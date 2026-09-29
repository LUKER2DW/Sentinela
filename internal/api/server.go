// Package api is the HTTP layer: JSON API, auth, stream proxy and SPA hosting.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/camera/dvrip"
	"github.com/LUKER2DW/sentinela/internal/config"
	"github.com/LUKER2DW/sentinela/internal/nas"
	"github.com/LUKER2DW/sentinela/internal/push"
	"github.com/LUKER2DW/sentinela/internal/store"
)

// Version is set by main.
var Version = "dev"

// Server holds API dependencies.
type Server struct {
	Cfg     *config.Config
	Store   *store.Store
	Log     *slog.Logger
	Web     fs.FS // built SPA (may be empty in dev)
	Started time.Time
	FFmpeg  string

	tickets  *auth.Tickets
	loginRL  *auth.Limiter
	go2rtc   *url.URL
	ctrl     map[string]*dvrip.Controller
	hub      *Hub
	stateMu  sync.Mutex
	stateTTL map[string]cachedState

	snaps snapshotter
	push  notifier

	// Nuvem file API (ADR-0012); nil when the share is not mounted.
	nas     *nas.Share
	nasRoot string
	thumbs  *nas.Thumbs
}

type cachedState struct {
	at time.Time
	st *dvrip.State
}

// New builds a server. Call Close on shutdown.
func New(cfg *config.Config, st *store.Store, web fs.FS, log *slog.Logger) (*Server, error) {
	g, err := url.Parse(cfg.Go2RTCAPI)
	if err != nil {
		return nil, err
	}
	s := &Server{Cfg: cfg, Store: st, Log: log, Web: web, Started: time.Now(), FFmpeg: "ffmpeg",
		tickets: auth.NewTickets(60 * time.Second), loginRL: auth.NewLimiter(5, time.Minute),
		go2rtc: g, ctrl: map[string]*dvrip.Controller{}, hub: NewHub(), stateTTL: map[string]cachedState{}}
	for i := range cfg.Cameras {
		cam := &cfg.Cameras[i]
		addr := hostPort(cam.Host, cam.DVRIPPort)
		s.ctrl[cam.ID] = dvrip.NewController(addr, cam.User, cam.Password, cam.InvertPanTilt, log.With("camera", cam.ID))
	}
	s.openNAS()
	if keys, err := push.LoadOrCreateKeys(filepath.Join(cfg.DataDir, "push", "vapid.json")); err != nil {
		log.Warn("notifications disabled: VAPID keys", "err", err)
	} else {
		s.push.sender = &push.Sender{Keys: keys, Subject: cfg.PushSubject}
	}
	return s, nil
}

// Hub returns the live event hub (fed by the ONVIF watchers).
func (s *Server) Hub() *Hub { return s.hub }

// Close releases camera sessions.
func (s *Server) Close() {
	for _, c := range s.ctrl {
		c.Close()
	}
	if s.nas != nil {
		s.nas.Close()
	}
}

// Handler returns the root handler with all routes and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	viewer := func(h http.HandlerFunc) http.Handler { return s.require(auth.RoleViewer, h) }
	operator := func(h http.HandlerFunc) http.Handler { return s.require(auth.RoleOperator, h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.require(auth.RoleAdmin, h) }

	mux.HandleFunc("GET /healthz", s.healthz)

	// Auth
	mux.HandleFunc("GET /api/setup", s.setupStatus)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.Handle("GET /api/me", viewer(s.me))
	mux.Handle("POST /api/me/password", viewer(s.changeOwnPassword))

	// Users (admin)
	mux.Handle("GET /api/users", admin(s.listUsers))
	mux.Handle("POST /api/users", admin(s.createUser))
	mux.Handle("PATCH /api/users/{id}", admin(s.updateUser))
	mux.Handle("DELETE /api/users/{id}", admin(s.deleteUser))
	mux.Handle("GET /api/audit", admin(s.listAudit))

	// Cameras and control
	mux.Handle("GET /api/cameras", viewer(s.listCameras))
	mux.Handle("GET /api/cameras/{id}/control", viewer(s.cameraControl))
	mux.Handle("GET /api/cameras/{id}/clock", viewer(s.cameraClock))
	mux.Handle("POST /api/cameras/{id}/ptz", operator(s.ptz))
	mux.Handle("POST /api/cameras/{id}/presets/{preset}", operator(s.preset))
	mux.Handle("PUT /api/cameras/{id}/light", operator(s.setLight))
	mux.Handle("PUT /api/cameras/{id}/daynight", operator(s.setDayNight))

	// Media
	mux.Handle("POST /api/cameras/{id}/stream-ticket", viewer(s.streamTicket))
	mux.Handle("GET /stream/ws", viewer(s.streamWS))
	mux.Handle("GET /api/cameras/{id}/timeline", viewer(s.timeline))
	mux.Handle("GET /api/recordings/{seg}", viewer(s.recording))
	mux.Handle("PUT /api/recordings/{seg}/protected", operator(s.protectSegment))
	mux.Handle("GET /api/cameras/{id}/export", viewer(s.export))

	// Events and system
	mux.Handle("GET /api/events", viewer(s.listEvents))
	mux.Handle("GET /api/events/stream", viewer(s.eventStream))
	mux.Handle("GET /api/events/{id}/snapshot", viewer(s.eventSnapshot))
	mux.Handle("GET /api/review", viewer(s.listReview))
	mux.Handle("POST /api/review/seen", viewer(s.markSeen))

	// Notifications (ADR-0013): every user manages their own devices;
	// operators change the house mode.
	mux.Handle("GET /api/push", viewer(s.pushInfo))
	mux.Handle("POST /api/push/subscribe", viewer(s.pushSubscribe))
	mux.Handle("POST /api/push/unsubscribe", viewer(s.pushUnsubscribe))
	mux.Handle("POST /api/push/test", viewer(s.pushTest))
	mux.Handle("GET /api/notify-mode", viewer(s.getMode))
	mux.Handle("PUT /api/notify-mode", operator(s.putMode))
	mux.Handle("GET /api/system", viewer(s.system))

	// Nuvem file API (ADR-0012): viewers read, operators and admins write.
	mux.Handle("GET /api/nas/list", viewer(s.nasList))
	mux.Handle("GET /api/nas/file", viewer(s.nasFile))
	mux.Handle("GET /api/nas/thumb", viewer(s.nasThumb))
	mux.Handle("GET /api/nas/upload", operator(s.nasUploadStatus))
	mux.Handle("PUT /api/nas/upload", operator(s.nasUpload))
	mux.Handle("POST /api/nas/mkdir", operator(s.nasMkdir))
	mux.Handle("POST /api/nas/move", operator(s.nasMove))
	mux.Handle("DELETE /api/nas/file", operator(s.nasDelete))

	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	}))
	mux.Handle("/", s.spa())

	return s.recoverer(s.securityHeaders(s.logRequests(s.session(s.csrf(mux)))))
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.DB().PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": Version})
}

func (s *Server) camera(w http.ResponseWriter, r *http.Request) *config.Camera {
	cam := s.Cfg.Camera(r.PathValue("id"))
	if cam == nil {
		writeErr(w, http.StatusNotFound, "camera not found")
	}
	return cam
}

func (s *Server) audit(r *http.Request, action, target string, ok bool, detail string) {
	u := userFrom(r)
	var id int64
	name := ""
	if u != nil {
		id, name = u.ID, u.Username
	}
	if err := s.Store.Audit(context.WithoutCancel(r.Context()), id, name, action, target, ok, detail); err != nil {
		s.Log.Warn("audit write failed", "err", err)
	}
}
