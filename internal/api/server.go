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

	tickets     *auth.Tickets
	loginRL     *auth.Limiter
	acctRL      *auth.Limiter // per-account login attempts in public zones (ADR-0019)
	failRL      *auth.Limiter // failed sign-ins / invite codes per address, per hour
	totpPending pendingTOTP
	go2rtc      *url.URL
	ctrl        map[string]*dvrip.Controller
	hub         *Hub
	stateMu     sync.Mutex
	stateTTL    map[string]cachedState

	snaps    snapshotter
	tlSem    chan struct{} // one timelapse at a time
	tl       tlJobs        // timelapses being written to disk
	det      detectorState // object detector sidecar (ADR-0016)
	frames   frameCache
	segs     segCache   // fragment indexes and timeline previews
	wizardMu sync.Mutex // one camera search/test at a time
	push     notifier

	// Self-hosted services: proxy to the apps agent (ADR-0021).
	hosting hostingState

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
		acctRL: auth.NewLimiter(10, time.Hour), failRL: auth.NewLimiter(20, time.Hour),
		go2rtc: g, ctrl: map[string]*dvrip.Controller{}, hub: NewHub(), stateTTL: map[string]cachedState{},
		tlSem: make(chan struct{}, 1)}
	for i := range cfg.Cameras {
		cam := &cfg.Cameras[i]
		addr := hostPort(cam.Host, cam.DVRIPPort)
		pan, tilt := cam.Inversion()
		s.ctrl[cam.ID] = dvrip.NewController(addr, cam.User, cam.Password, dvrip.Invert{Pan: pan, Tilt: tilt}, log.With("camera", cam.ID))
	}
	s.det.motion, s.det.open = map[string]bool{}, map[string]int64{}
	if tok, err := loadDetectorToken(cfg.DataDir); err != nil {
		log.Warn("object detector disabled: token", "err", err)
	} else {
		s.det.token = tok
	}
	s.openNAS()
	s.openHosting()
	if keys, err := push.LoadOrCreateKeys(filepath.Join(cfg.DataDir, "push", "vapid.json")); err != nil {
		log.Warn("notifications disabled: VAPID keys", "err", err)
	} else {
		s.push.sender = &push.Sender{Keys: keys, Subject: cfg.PushSubject}
	}
	if sender, err := push.LoadFCM(filepath.Join(cfg.DataDir, "fcm", "service-account.json")); err != nil {
		log.Warn("Firebase notifications unavailable", "err", err)
	} else {
		s.push.fcm = sender
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

// Handler returns the root handler for the private listener (LAN, and the
// tailnet through `tailscale serve` on loopback).
func (s *Server) Handler() http.Handler { return s.handler("") }

// PublicHandler serves a public listener (ZoneFunnel or ZonePublic,
// ADR-0019): same routes, mandatory 2FA and the closed door.
func (s *Server) PublicHandler(zone string) http.Handler {
	if !PublicZone(zone) {
		panic("api: PublicHandler needs a public zone") // programming error, caught at startup
	}
	return s.handler(zone)
}

func (s *Server) handler(zone string) http.Handler {
	mux := http.NewServeMux()
	viewer := func(h http.HandlerFunc) http.Handler { return s.require(auth.RoleViewer, h) }
	operator := func(h http.HandlerFunc) http.Handler { return s.require(auth.RoleOperator, h) }
	admin := func(h http.HandlerFunc) http.Handler { return s.require(auth.RoleAdmin, h) }

	mux.HandleFunc("GET /healthz", s.healthz)

	// Auth
	mux.HandleFunc("GET /api/setup", s.setupStatus)
	mux.HandleFunc("POST /api/setup", privateOnly(s.setup))
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/invite/accept", s.acceptInvite)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.Handle("GET /api/me", viewer(s.me))
	mux.Handle("POST /api/me/password", viewer(s.changeOwnPassword))
	mux.Handle("GET /api/me/sessions", viewer(s.mySessions))
	mux.Handle("POST /api/me/avatar", viewer(s.putAvatar))
	mux.Handle("DELETE /api/me/avatar", viewer(s.deleteAvatar))
	mux.Handle("GET /api/avatars/{username}", viewer(s.getAvatar))
	mux.Handle("DELETE /api/me/sessions/{id}", viewer(s.revokeSession))
	// Two-step login (ADR-0019): enrolled at home or over the tailnet.
	mux.Handle("POST /api/me/totp/setup", viewer(privateOnly(s.totpSetup)))
	mux.Handle("POST /api/me/totp/enable", viewer(privateOnly(s.totpEnable)))
	mux.Handle("POST /api/me/totp/disable", viewer(privateOnly(s.totpDisable)))

	// Users (admin)
	mux.Handle("GET /api/users", admin(s.listUsers))
	mux.Handle("POST /api/users", admin(privateOnly(s.createUser)))
	mux.Handle("GET /api/users/{id}", admin(s.userDetail))
	mux.Handle("PATCH /api/users/{id}", admin(privateOnly(s.updateUser)))
	mux.Handle("POST /api/users/{id}/invite", admin(privateOnly(s.reinvite)))
	mux.Handle("DELETE /api/users/{id}", admin(privateOnly(s.deleteUser)))
	mux.Handle("GET /api/audit", admin(s.listAudit))
	mux.Handle("POST /api/cameras/discover", admin(s.discoverCameras))
	mux.Handle("POST /api/cameras/test", admin(s.testCamera))

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
	mux.Handle("GET /api/cameras/{id}/timelapse", viewer(s.timelapse))
	mux.Handle("GET /api/cameras/{id}/timelapse/prepare", viewer(s.timelapsePrepare))
	mux.Handle("GET /api/cameras/{id}/frame.jpeg", viewer(s.frame))
	mux.Handle("GET /api/cameras/{id}/detection", viewer(s.getDetection))
	mux.Handle("PUT /api/cameras/{id}/detection", operator(s.putDetection))

	// Object detector sidecar (loopback + token, ADR-0016).
	mux.Handle("GET /internal/detector/config", s.detectorOnly(s.detectorConfig))
	mux.Handle("GET /internal/detector/motion", s.detectorOnly(s.detectorMotion))
	mux.Handle("POST /internal/detector/event", s.detectorOnly(s.detectorEvent))
	mux.Handle("POST /internal/detector/status", s.detectorOnly(s.detectorStatus))

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
	mux.Handle("GET /api/notify/stream", viewer(s.notifyStream))
	mux.Handle("GET /api/notify/fcm", viewer(s.fcmStatus))
	mux.Handle("PUT /api/notify/fcm/token", viewer(s.registerFCM))
	mux.Handle("DELETE /api/notify/fcm/token", viewer(s.unregisterFCM))
	mux.Handle("GET /api/notify/pending/{id}", viewer(s.pendingNotification))
	mux.Handle("GET /api/me/notify", viewer(s.getNotifyPrefs))
	mux.Handle("PUT /api/me/notify", viewer(s.putNotifyPrefs))
	mux.Handle("GET /api/notify-mode", viewer(s.getMode))
	mux.Handle("PUT /api/notify-mode", operator(s.putMode))
	mux.Handle("GET /api/system", viewer(s.system))
	// Android app self-update (the APK is sideloaded, no Play Store).
	mux.Handle("GET /api/app/latest", viewer(s.appLatest))
	mux.Handle("GET /api/app/sentinela.apk", viewer(s.appAPK))

	// Nuvem file API (ADR-0012): operators use their own folder and the
	// shared one, admins everything; viewers only see cameras (round 6).
	mux.Handle("GET /api/nas/list", operator(s.nasList))
	mux.Handle("GET /api/nas/file", operator(s.nasFile))
	mux.Handle("GET /api/nas/thumb", operator(s.nasThumb))
	mux.Handle("GET /api/nas/upload", operator(s.nasUploadStatus))
	mux.Handle("PUT /api/nas/upload", operator(s.nasUpload))
	mux.Handle("POST /api/nas/mkdir", operator(s.nasMkdir))
	mux.Handle("PUT /api/nas/text", operator(s.nasText))
	mux.Handle("POST /api/nas/move", operator(s.nasMove))
	mux.Handle("DELETE /api/nas/file", operator(s.nasDelete))
	mux.Handle("DELETE /api/nas/trash", operator(s.nasPurge))

	// Self-hosted services (ADR-0021): admins only; changes from home or
	// the tailnet (hostingProxy), every change audited.
	mux.Handle("/api/hosting/", admin(s.hostingProxy))

	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	}))
	mux.Handle("/", s.spa())

	return s.recoverer(s.securityHeaders(withZone(zone, s.logRequests(s.session(s.closedDoor(s.csrf(mux)))))))
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
	if cam == nil || !s.canSee(r, cam.ID) {
		writeErr(w, http.StatusNotFound, "camera not found")
		return nil
	}
	return cam
}

// canSee reports whether the signed-in user may see a camera. Hidden
// cameras answer 404, as if they did not exist.
func (s *Server) canSee(r *http.Request, cameraID string) bool {
	u := userFrom(r)
	return u != nil && u.CanSee(cameraID)
}

// visibleEvents drops events of cameras the user may not see.
func (s *Server) visibleEvents(r *http.Request, ev []store.Event) []store.Event {
	out := ev[:0]
	for _, e := range ev {
		if s.canSee(r, e.CameraID) {
			out = append(out, e)
		}
	}
	return out
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
