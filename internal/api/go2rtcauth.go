package api

import "net/http"

// go2rtcUser is the account go2rtc's API accepts (deploy/go2rtc.yaml). The
// API listens on the host loopback, which every host-network container
// shares (the detector among them), so it answers only callers with the
// password, loopback ones too (local_auth). serve is its only client.
const go2rtcUser = "sentinela"

// go2rtcAuth replaces any credential on a request bound for go2rtc with
// serve's own: a browser's Authorization header never reaches go2rtc.
func (s *Server) go2rtcAuth(r *http.Request) {
	r.Header.Del("Authorization")
	if s.Go2RTCPassword != "" {
		r.SetBasicAuth(go2rtcUser, s.Go2RTCPassword)
	}
}
