package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/LUKER2DW/sentinela/internal/auth"
	"github.com/LUKER2DW/sentinela/internal/auth/redact"
	"github.com/LUKER2DW/sentinela/internal/camera/dvrip"
	"github.com/LUKER2DW/sentinela/internal/camera/onvif"
	"github.com/LUKER2DW/sentinela/internal/config"
)

type cameraDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Recording   bool   `json:"recording"`
	RecordAudio bool   `json:"record_audio"`
	Retention   int    `json:"retention_days"`
	// Control: camera settings over DVRIP (day/night, clock; XMEye only).
	Control bool   `json:"control"`
	Vendor  string `json:"vendor"`
	// What the camera has (config features): the UI shows only these controls.
	PTZ   bool `json:"ptz"`
	Light bool `json:"light"`
	Talk  bool `json:"talk"`
	// PTZDriver: "dvrip", "onvif" (no focus/iris) or "" (no PTZ).
	PTZDriver string `json:"ptz_driver"`
	// ONVIF: the server talks ONVIF to it (events, clock).
	ONVIF bool `json:"onvif"`
}

// ptzDriver moves a camera: the DVRIP controller (XMEye) or ONVIF PTZ.
type ptzDriver interface {
	Start(ctx context.Context, op, dir string, speed int) error
	Stop(ctx context.Context) error
	Nudge(ctx context.Context, op, dir string, speed int, d time.Duration) error
	Preset(ctx context.Context, action string, id int) error
}

func (s *Server) listCameras(w http.ResponseWriter, r *http.Request) {
	out := make([]cameraDTO, 0, len(s.Cfg.Cameras))
	for _, c := range s.Cfg.Cameras {
		if !s.canSee(r, c.ID) {
			continue
		}
		out = append(out, cameraDTO{ID: c.ID, Name: c.Name, Recording: c.Recording(), RecordAudio: c.AudioRecording(), Retention: c.RetentionDays,
			Control: c.HasDVRIP(), Vendor: c.Vendor, PTZ: c.HasPTZ(), Light: c.HasLight(), Talk: c.HasTalk(),
			PTZDriver: c.PTZDriver(), ONVIF: c.UsesONVIF()})
	}
	writeJSON(w, http.StatusOK, out)
}

// cameraCtrl is s.camera for DVRIP control routes: cameras of other
// vendors answer 501 (view and record only).
func (s *Server) cameraCtrl(w http.ResponseWriter, r *http.Request) (*config.Camera, *dvrip.Controller) {
	cam := s.camera(w, r)
	if cam == nil {
		return nil, nil
	}
	ctrl := s.ctrl[cam.ID]
	if ctrl == nil {
		writeErr(w, http.StatusNotImplemented, "this camera has no remote control (vendor "+cam.Vendor+")")
		return nil, nil
	}
	return cam, ctrl
}

// camErr converts a driver error into a safe message and HTTP status.
func (s *Server) camErr(w http.ResponseWriter, cam *config.Camera, err error) {
	msg := redact.Secrets(err.Error(), cam.Password)
	s.Log.Warn("camera control failed", "camera", cam.ID, "err", msg)
	switch {
	case errors.Is(err, dvrip.ErrInvalidCommand), errors.Is(err, onvif.ErrInvalidCommand):
		writeErr(w, http.StatusBadRequest, "invalid command")
	case errors.Is(err, onvif.ErrUnsupported):
		writeErr(w, http.StatusNotImplemented, "this camera does not do that over ONVIF")
	case errors.Is(err, onvif.ErrNoPTZ):
		writeErr(w, http.StatusNotImplemented, "the camera reports no ONVIF PTZ")
	case errors.Is(err, dvrip.ErrUnsupportedModel):
		writeErr(w, http.StatusForbidden, "control not approved for this camera model")
	case errors.Is(err, dvrip.ErrUnresponsive):
		writeErr(w, http.StatusServiceUnavailable, "camera is busy or not answering, try again in a few seconds")
	case errors.Is(err, dvrip.ErrRejected):
		writeErr(w, http.StatusBadGateway, "camera rejected the command")
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "camera did not answer in time")
	default:
		writeErr(w, http.StatusBadGateway, "camera unreachable")
	}
}

// ptzCam is s.camera for PTZ routes: cameras without PTZ answer 501.
func (s *Server) ptzCam(w http.ResponseWriter, r *http.Request) (*config.Camera, ptzDriver) {
	cam := s.camera(w, r)
	if cam == nil {
		return nil, nil
	}
	drv := s.ptzDrv[cam.ID]
	if drv == nil {
		writeErr(w, http.StatusNotImplemented, "this camera has no PTZ")
		return nil, nil
	}
	return cam, drv
}

// Camera reads coalesced per camera (camReads): viewers can ask as often as
// they like, the camera answers at most one read at a time, and its answer
// is reused for a while.
var (
	// controlRead: the SPA reads /control with a 60 s stale time. Viewers
	// always get an answer up to 15 s old; ?fresh=1 from an operator or
	// admin accepts one up to 2 s old, so even that cannot keep the camera's
	// control lock busy ahead of PTZ commands and the deadman.
	controlRead  = readPolicy{maxAge: 15 * time.Second, errAge: 5 * time.Second, timeout: 8 * time.Second}
	controlFresh = 2 * time.Second
	// clockRead: the offset of a camera clock does not move in seconds;
	// the SPA asks once a minute per open view.
	clockRead = readPolicy{maxAge: 10 * time.Second, errAge: 5 * time.Second, timeout: 5 * time.Second}
)

// controlPolicy is controlRead, shortened by ?fresh for operators.
func controlPolicy(r *http.Request) readPolicy {
	p := controlRead
	if u := userFrom(r); r.URL.Query().Get("fresh") != "" && u != nil && auth.RoleAtLeast(u.Role, auth.RoleOperator) {
		p.maxAge = controlFresh
	}
	return p
}

func (s *Server) cameraControl(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	ctrl := s.ctrl[cam.ID]
	if ctrl == nil && s.onvifPTZ[cam.ID] != nil {
		s.onvifControl(w, r, cam)
		return
	}
	if ctrl == nil {
		writeErr(w, http.StatusNotImplemented, "this camera has no remote control (vendor "+cam.Vendor+")")
		return
	}
	st, err := s.control.get(r.Context(), cam.ID, controlPolicy(r), ctrl.State)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// onvifControl answers /control for a camera moved over ONVIF PTZ: its
// saved positions (the DVRIP-only settings are absent).
func (s *Server) onvifControl(w http.ResponseWriter, r *http.Request, cam *config.Camera) {
	p := s.onvifPTZ[cam.ID]
	st, err := s.control.get(r.Context(), cam.ID, controlPolicy(r), func(ctx context.Context) (*dvrip.State, error) {
		list, err := p.Presets(ctx)
		if err != nil {
			return nil, err
		}
		st := &dvrip.State{Approved: true, Presets: make([]dvrip.Preset, 0, len(list))}
		for _, pr := range list {
			st.Presets = append(st.Presets, dvrip.Preset{ID: pr.ID, Name: pr.Name})
		}
		return st, nil
	})
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) invalidateState(id string) { s.control.invalidate(id) }

func (s *Server) ptz(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.ptzCam(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Action string `json:"action"` // start (also keepalive), stop, nudge
		Op     string `json:"op"`     // move, zoom, focus, iris
		Dir    string `json:"dir"`
		Speed  int    `json:"speed"`
		Ms     int    `json:"ms"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dvrip.DefaultTimeout+time.Second)
	defer cancel()
	var err error
	if body.Action != "stop" {
		s.takeControl(r, cam.ID)
	}
	s.ptzQuiet.hold(cam.ID, ptzSettle)
	switch body.Action {
	case "start":
		err = ctrl.Start(ctx, body.Op, body.Dir, body.Speed)
	case "stop":
		// Stop must go through even if the client went away.
		ctx2, cancel2 := context.WithTimeout(context.WithoutCancel(r.Context()), dvrip.DefaultTimeout)
		defer cancel2()
		err = ctrl.Stop(ctx2)
	case "nudge":
		err = ctrl.Nudge(ctx, body.Op, body.Dir, body.Speed, time.Duration(body.Ms)*time.Millisecond)
	default:
		err = dvrip.ErrInvalidCommand
	}
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) preset(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.ptzCam(w, r)
	if cam == nil {
		return
	}
	id, err := strconv.Atoi(r.PathValue("preset"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad preset id")
		return
	}
	var body struct {
		Action string `json:"action"` // goto, set, clear
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.takeControl(r, cam.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 2*dvrip.DefaultTimeout)
	defer cancel()
	if body.Action == "goto" {
		s.ptzQuiet.hold(cam.ID, presetSettle)
	}
	err = ctrl.Preset(ctx, body.Action, id)
	if body.Action != "goto" {
		s.audit(r, "camera.preset."+body.Action, fmt.Sprintf("%s#%d", cam.ID, id), err == nil, "")
		s.invalidateState(cam.ID)
	}
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setLight(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	if !cam.HasLight() {
		writeErr(w, http.StatusNotImplemented, "this camera has no white light")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*dvrip.DefaultTimeout)
	defer cancel()
	err := ctrl.SetWhiteLight(ctx, body.On)
	s.audit(r, "camera.white_light", cam.ID, err == nil, strconv.FormatBool(body.On))
	s.invalidateState(cam.ID)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) setDayNight(w http.ResponseWriter, r *http.Request) {
	cam, ctrl := s.cameraCtrl(w, r)
	if cam == nil {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*dvrip.DefaultTimeout)
	defer cancel()
	err := ctrl.SetDayNight(ctx, body.Mode)
	s.audit(r, "camera.day_night", cam.ID, err == nil, body.Mode)
	s.invalidateState(cam.ID)
	if err != nil {
		s.camErr(w, cam, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// clockReading is what /clock answers.
type clockReading struct {
	Camera   time.Time `json:"camera"`
	Server   time.Time `json:"server"`
	OffsetMS int64     `json:"offset_ms"`
}

// cameraClock reports how far the camera's clock (the one burned into the
// video) is from the server's NTP-synced clock. Read-only ONVIF call, made
// at most once at a time per camera and reused for a few seconds.
func (s *Server) cameraClock(w http.ResponseWriter, r *http.Request) {
	cam := s.camera(w, r)
	if cam == nil {
		return
	}
	if !cam.UsesONVIF() {
		writeErr(w, http.StatusNotImplemented, "ONVIF is off for this camera")
		return
	}
	reading, err := s.clock.get(r.Context(), cam.ID, clockRead, func(ctx context.Context) (clockReading, error) {
		c := onvif.NewClient(cam.Host, cam.ONVIFPort, cam.User, cam.Password).WithDevicePath(cam.ONVIFPath)
		t0 := time.Now()
		camTime, err := c.SystemTime(ctx)
		t1 := time.Now()
		if err != nil {
			s.Log.Warn("camera clock read failed", "camera", cam.ID, "err", redact.Secrets(err.Error(), cam.Password))
			return clockReading{}, err
		}
		mid := t0.Add(t1.Sub(t0) / 2)
		// The camera reports whole seconds (truncated): on average its real
		// time is half a second later, so the offset is accurate to ±0.5 s.
		return clockReading{Camera: camTime, Server: mid.UTC(), OffsetMS: camTime.Add(500 * time.Millisecond).Sub(mid).Milliseconds()}, nil
	})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "camera did not report its time")
		return
	}
	writeJSON(w, http.StatusOK, reading)
}
