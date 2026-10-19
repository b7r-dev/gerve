// Package server wires the HTTP surface: routing, per-request repo access,
// and the shared page context. Handlers never touch go-git types directly —
// everything flows through internal/git.
package server

import (
	"io/fs"
	"log"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/b7r-dev/gerve/internal/git"
	"github.com/b7r-dev/gerve/internal/render"
	"github.com/b7r-dev/gerve/web"
)

// Options configures a Server.
type Options struct {
	// DefaultRef is the ref to open at when the URL omits one (default HEAD).
	DefaultRef string
	// Version is shown in the footer and -V.
	Version string
}

// Server is the fully constructed application. It holds no per-request state.
type Server struct {
	repo   *git.Repo
	rnd    *render.Renderer
	def    string
	ver    string
	mux    *http.ServeMux
	static http.Handler
}

// New builds the server and its routes.
func New(repo *git.Repo, rnd *render.Renderer, opts Options) *Server {
	s := &Server{
		repo: repo,
		rnd:  rnd,
		def:  opts.DefaultRef,
		ver:  opts.Version,
	}
	if s.def == "" {
		s.def = "HEAD"
	}

	if sub, err := fs.Sub(web.Static(), "static"); err == nil {
		s.static = http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /tree/", s.tree)
	mux.HandleFunc("GET /blob/", s.blob)
	mux.HandleFunc("GET /blame/", s.blame)
	mux.HandleFunc("GET /raw/", s.raw)
	mux.HandleFunc("GET /commits/", s.commits)
	mux.HandleFunc("GET /commit/", s.commit)
	mux.HandleFunc("GET /history/", s.history)
	mux.HandleFunc("GET /refs", s.refsPage)
	mux.HandleFunc("GET /static/", s.serveStatic)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", s.notFound)
	s.mux = mux
	return s
}

// Handler returns the middleware-wrapped root handler.
func (s *Server) Handler() http.Handler {
	return s.logMW(s.recoverMW(s.mux))
}

// --- middleware --------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (s *Server) logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		log.Printf("%s %s %d", r.Method, r.URL.RequestURI(), sw.code)
	})
}

func (s *Server) recoverMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic serving %s: %v\n%s", r.URL.Path, rec, debug.Stack())
				if sw.code == 0 {
					s.errorPage(sw, r, http.StatusInternalServerError,
						"internal error", "Something went wrong handling this request. The server is still running.")
				}
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// --- shared page context -----------------------------------------------------

func (s *Server) newBase(ref, path, route string) render.Base {
	refs, err := s.repo.Refs()
	if err != nil {
		refs = nil
	}
	return render.Base{
		Version:  s.ver,
		RepoName: s.repo.Name(),
		Ref:      ref,
		Head:     s.repo.Head(),
		Refs:     refs,
		Route:    route,
		Path:     path,
		Crumbs:   s.crumbs(ref, path),
	}
}

func (s *Server) crumbs(ref, path string) []render.Crumb {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	out := make([]render.Crumb, 0, len(parts))
	acc := ""
	for i, seg := range parts {
		if i > 0 {
			acc += "/"
		}
		acc += seg
		if i == len(parts)-1 {
			out = append(out, render.Crumb{Name: seg})
		} else {
			out = append(out, render.Crumb{Name: seg, URL: render.URLFor("tree", ref, acc)})
		}
	}
	return out
}

// --- helpers -----------------------------------------------------------------

// splitRefPath parses "/{ref}/{path...}" remainder, matching the ref
// greedily against known revisions so slashed branch names work.
func (s *Server) splitRefPath(rest string) (ref, path string, ok bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", "", false
	}
	segs := strings.Split(rest, "/")
	for _, seg := range segs {
		switch seg {
		case "", ".", "..", ".git":
			return "", "", false
		}
	}
	for i := len(segs); i >= 1; i-- {
		cand := strings.Join(segs[:i], "/")
		if _, err := s.repo.Resolve(cand); err == nil {
			return cand, strings.Join(segs[i:], "/"), true
		}
	}
	return "", "", false
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, code int, title, msg string) {
	s.page(w, r, code, "error", render.ErrorPage{
		Base:    s.newBase(s.def, "", "tree"),
		Code:    code,
		Title:   title,
		Message: msg,
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusNotFound, "not found",
		"The page you asked for does not exist. Paths look like /tree/{ref}/{path}.")
}
