// Command gerve serves a read-only web UI for a local git repository.
//
//	gerve -p 8080 .
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
	"github.com/b7r-dev/gerve/internal/server"
)

// version is set at link time by GoReleaser. Local builds report "dev".
var version = "dev"

const usage = `gerve — a read-only web UI for local git repositories

usage: gerve [flags] [path]

  -p, --port <port>     listen port (default 8080; 0 = OS-assigned)
  -a, --addr <addr>     bind address (default 127.0.0.1)
  -b, --ref <ref>       ref to open at (default HEAD)
      --open            open the browser on start
      --share-url <url> public URL for LAN/QR sharing
  -V, --version         print version
  -h, --help            show this help

Flags may appear before or after the path: 'gerve . -p 8080' works too.
`

type cliOpts struct {
	port     int
	addr     string
	ref      string
	path     string
	shareURL string
	open     bool
	version  bool
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gerve:", err)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if opts.version {
		fmt.Println("gerve", version)
		return
	}
	if opts.path == "" {
		opts.path = "."
	}
	abs, err := filepath.Abs(opts.path)
	if err != nil {
		fatal(err)
	}

	repo, err := git.Open(abs)
	if err != nil {
		if errors.Is(err, git.ErrNotRepo) {
			fmt.Fprintf(os.Stderr, "gerve: not a git repository: %s (no .git found)\n", opts.path)
			os.Exit(1)
		}
		fatal(err)
	}

	rnd, err := render.New()
	if err != nil {
		fatal(err)
	}

	addr := net.JoinHostPort(opts.addr, strconv.Itoa(opts.port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "address already in use") {
			fmt.Fprintf(os.Stderr, "gerve: port %d busy — try: gerve -p %d\n", opts.port, opts.port+1)
			os.Exit(1)
		}
		fatal(err)
	}

	local := displayURL(ln, opts.addr)
	_, actualPort, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(actualPort)
	shareURL, err := normalizeShareURL(opts.shareURL, port)
	if err != nil {
		ln.Close()
		fmt.Fprintf(os.Stderr, "gerve: invalid --share-url: %v\n", err)
		os.Exit(2)
	}

	var origins []string
	var lan []string
	if isAnyBind(opts.addr) {
		lan = lanURLs(ln)
		origins = shareOrigins(shareURL, lan)
	} else if shareURL != "" {
		origins = []string{shareURL}
	}
	share := server.ShareOptions{
		Enabled:   len(origins) > 0,
		PublicURL: shareURL,
		Origins:   origins,
	}
	srv := server.New(repo, rnd, server.Options{
		DefaultRef: opts.ref,
		Version:    version,
		Share:      share,
	})

	fmt.Printf("gerve %s · %s%s at %s\n", version, repo.Name(), bareNote(repo), local)
	if isAnyBind(opts.addr) {
		for _, u := range lan {
			fmt.Printf("%*s also on %s\n", len("gerve "+version+" · "), "", u)
		}
		fmt.Println("note: reachable from your network — anyone who can reach this machine can read this repository")
	}
	if shareURL != "" {
		fmt.Printf("%*s share  %s\n", len("gerve "+version+" · "), "", shareURL)
		if !isAnyBind(opts.addr) {
			fmt.Println("note: share URL is set while bound to loopback — a proxy must forward to gerve")
		}
	}
	if opts.open {
		go openBrowser(local)
	}

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		hs.Shutdown(shutdownCtx)
	}()

	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func parseArgs(args []string) (cliOpts, error) {
	o := cliOpts{port: 8080, addr: "127.0.0.1", ref: "HEAD"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-p", "--port":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("-%s needs a port number", strings.TrimLeft(a, "-"))
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 || n > 65535 {
				return o, fmt.Errorf("invalid port %q", args[i])
			}
			o.port = n
		case "-a", "--addr":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("-%s needs an address", strings.TrimLeft(a, "-"))
			}
			o.addr = args[i]
		case "-b", "--branch", "--ref":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("-%s needs a ref name", strings.TrimLeft(a, "-"))
			}
			o.ref = args[i]
		case "--open":
			o.open = true
		case "--share-url":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--share-url needs a URL")
			}
			o.shareURL = args[i]
		case "-V", "--version":
			o.version = true
		case "-h", "--help":
			fmt.Print(usage)
			os.Exit(0)
		default:
			if strings.HasPrefix(a, "-") && a != "-" && a != "--" {
				return o, fmt.Errorf("unknown flag %q", a)
			}
			if o.path != "" {
				return o, fmt.Errorf("unexpected extra argument %q", a)
			}
			o.path = a
		}
	}
	return o, nil
}

func displayURL(ln net.Listener, bind string) string {
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	if isAnyBind(bind) {
		return "http://127.0.0.1:" + port
	}
	if host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// isAnyBind reports whether the bind address reaches beyond loopback.
func isAnyBind(bind string) bool {
	return bind == "" || bind == "0.0.0.0" || bind == "::" || bind == "[::]"
}

// shareOrigins returns the public URL followed by reachable LAN URLs, with
// duplicates removed. A public URL and a LAN address can name the same origin.
func shareOrigins(public string, lan []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lan)+1)
	add := func(u string) {
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	add(public)
	for _, u := range lan {
		add(u)
	}
	return out
}

// lanURLs returns http URLs for every non-loopback, non-link-local address
// of this machine, so the user knows what to type on another device. IPv4
// comes first because a phone is more likely to reach it when both exist.
func lanURLs(ln net.Listener) []string {
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return nil
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var v4, v6 []string
	add := func(ip net.IP) {
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return
		}
		host := ip.String()
		is4 := ip.To4() != nil
		if is4 {
			host = ip.To4().String()
		}
		u := "http://" + net.JoinHostPort(host, port)
		if seen[u] {
			return
		}
		seen[u] = true
		if is4 {
			v4 = append(v4, u)
		} else {
			v6 = append(v6, u)
		}
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				add(v.IP)
			case *net.IPAddr:
				add(v.IP)
			}
		}
	}
	return append(v4, v6...)
}

func bareNote(r *git.Repo) string {
	if r.HasWorktree() {
		return ""
	}
	return " (bare)"
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	cmd.Run() // best effort
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gerve:", err)
	os.Exit(1)
}
