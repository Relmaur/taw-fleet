package review

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status is how a publish is going, for the page's progress.
type Status struct {
	State   string  `json:"state"` // review, publishing, published, failed, cancelled
	Step    string  `json:"step,omitempty"`
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Message string  `json:"message,omitempty"`
	Result  *Result `json:"result,omitempty"`
	Kept    string  `json:"kept,omitempty"` // where the review is kept (review.html), once it ended
}

// Decision is what the person did on the page: published (with the
// decisions) or cancelled.
type Decision struct {
	Cancel    bool
	Decisions Decisions
}

// Server serves one review on 127.0.0.1, at a path only this Mac's browser
// is given (a random token), until Close. It shows the page and Local's
// files the plan uses (nothing else on disk), and takes one decision.
type Server struct {
	URL string

	page      Page
	uploads   string
	allowed   map[string]bool
	token     string
	host      string
	srv       *http.Server
	decisions chan Decision
	decided   bool
	mu        sync.Mutex
	status    Status
}

// Serve starts the review. uploads is Local's uploads folder (its files
// the publish brings are shown from there).
func Serve(ctx context.Context, page Page, uploads string) (*Server, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		_ = ln.Close()
		return nil, err
	}
	s := &Server{page: page, uploads: uploads, allowed: map[string]bool{}, token: hex.EncodeToString(b), host: ln.Addr().String(),
		decisions: make(chan Decision, 1), status: Status{State: "review"}}
	for _, f := range page.Files {
		s.allowed[f.Path] = true
	}
	for _, g := range page.Groups {
		for _, r := range g.Records {
			for _, c := range r.Changes {
				for _, m := range c.NewMedia {
					if m.Incoming {
						s.allowed[m.Src] = true
					}
				}
			}
		}
	}
	s.URL = "http://" + s.host + "/" + s.token + "/"
	mux := http.NewServeMux()
	mux.HandleFunc("/"+s.token+"/", s.handle)
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Decisions delivers the person's one decision.
func (s *Server) Decisions() <-chan Decision { return s.decisions }

// SetStatus tells the page how the publish is going.
func (s *Server) SetStatus(st Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
}

// Close stops the server (after a moment, so the page reads the last status).
func (s *Server) Close(grace time.Duration) {
	go func() {
		time.Sleep(grace)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(ctx)
	}()
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// Only this address: a page elsewhere can't reach it by another name
	// (DNS rebinding), and a POST must come from the review itself.
	if r.Host != s.host {
		http.Error(w, "not here", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+s.host {
			http.Error(w, "not from the review", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "JSON only", http.StatusUnsupportedMediaType)
			return
		}
	}
	rest := strings.TrimPrefix(r.URL.Path, "/"+s.token+"/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		html, err := s.page.HTML()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src * data: blob:; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
		_, _ = w.Write(html)
	case strings.HasPrefix(rest, "media/") && r.Method == http.MethodGet:
		s.media(w, r, strings.TrimPrefix(rest, "media/"))
	case rest == "status" && r.Method == http.MethodGet:
		s.mu.Lock()
		st := s.status
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(st)
	case rest == "publish" && r.Method == http.MethodPost:
		var body struct {
			Include []string                     `json:"include"`
			Sides   map[string]map[string]string `json:"sides"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
			reply(w, http.StatusBadRequest, "That isn't a decision.")
			return
		}
		d, missing := s.page.Decide(body.Include, body.Sides)
		switch {
		case len(missing) > 0:
			reply(w, http.StatusBadRequest, "Decide these first: "+strings.Join(missing, "; "))
			return
		case len(d.Approved) == 0:
			reply(w, http.StatusBadRequest, "Nothing is ticked to publish.")
			return
		}
		if !s.decide(Decision{Decisions: d}) {
			reply(w, http.StatusConflict, "This review was already decided.")
			return
		}
		s.SetStatus(Status{State: "publishing", Step: "upload", Message: "Starting."})
		reply(w, http.StatusAccepted, "Publishing.")
	case rest == "cancel" && r.Method == http.MethodPost:
		s.decide(Decision{Cancel: true})
		s.SetStatus(Status{State: "cancelled", Message: "Nothing was published."})
		reply(w, http.StatusOK, "Cancelled.")
	default:
		http.NotFound(w, r)
	}
}

// decide hands over the first decision only.
func (s *Server) decide(d Decision) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decided {
		return false
	}
	s.decided = true
	s.decisions <- d
	return true
}

// media serves one of Local's files the plan uses, from the uploads folder.
func (s *Server) media(w http.ResponseWriter, r *http.Request, rel string) {
	if !s.allowed[rel] {
		http.NotFound(w, r)
		return
	}
	file, err := Within(s.uploads, rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, file)
}

// Within is rel inside root, never outside it (no "..", no absolute path, no
// symlink out).
func Within(root, rel string) (string, error) {
	if rel == "" || strings.Contains(rel, "\\") || filepath.IsAbs(rel) {
		return "", errors.New("not a path under the folder")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", errors.New("outside the folder")
	}
	full := filepath.Join(root, clean)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) {
		return "", errors.New("outside the folder")
	}
	if fi, err := os.Stat(resolved); err != nil || !fi.Mode().IsRegular() {
		return "", errors.New("not a file")
	}
	return resolved, nil
}

func reply(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}
