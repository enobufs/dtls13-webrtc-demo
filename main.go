// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
//go:build !js

// DTLS 1.3 <-> browser WebRTC demo.
//
// A minimal Pion signaling server that answers a browser's offer and negotiates
// DTLS 1.3 (RFC 9147). The browser publishes a synthetic video track; the server
// receives it (SRTP decrypt) and loops it back on its own track (SRTP encrypt).
// The browser rendering the returned video is the end-to-end proof that SRTP
// works in BOTH directions over DTLS 1.3.
//
// Why this needs patched Pion: DTLS 1.3 lives only on unreleased pion/dtls, and
// its public keying-material exporter (RFC 5705 / RFC 8446 s7.5) was DTLS
// 1.2-only, so SRTP-over-DTLS-1.3 could not be keyed. This demo builds against a
// 1.3-capable pion/dtls plus a small exporter patch, and a pion/webrtc patch that
// exposes a DTLS version knob (SettingEngine.SetDTLSVersionRange). See README.md.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pion/dtls/v3/pkg/protocol"
	"github.com/pion/interceptor"
	"github.com/pion/logging"
	"github.com/pion/webrtc/v4"
)

const listenAddr = ":8080"

func main() {
	var pc *webrtc.PeerConnection
	setupOfferHandler(&pc)
	setupCandidateHandler(&pc)
	setupStaticHandler()

	maxLabel := "1.3"
	if os.Getenv("DEMO_DTLS_MAX") == "12" {
		maxLabel = "1.2 (DEMO_DTLS_MAX=12)"
	}
	fmt.Println("DTLS 1.3 <-> browser WebRTC demo — Pion answerer")
	fmt.Printf("Requesting DTLS max version: %s\n", maxLabel)
	fmt.Println("Open a Chromium-based browser at http://localhost:8080 and click Start.")
	fmt.Println("The server logs the actual negotiated DTLS version once the handshake")
	fmt.Println("completes, e.g. \"DTLS handshake complete, negotiated version=1.3 ...\"")
	fmt.Println("(negotiated version=1.2 with DEMO_DTLS_MAX=12).")
	fmt.Println("Set DEMO_LOG=debug (or trace) for verbose Pion logs.")
	fmt.Printf("Signaling server on http://localhost%s\n", listenAddr)

	//nolint:gosec // G114: demo server, no timeouts needed
	if err := http.ListenAndServe(listenAddr, nil); err != nil {
		fmt.Printf("Failed to start server: %v\n", err)
	}
}

func newAPI() *webrtc.API {
	// A MediaEngine with default codecs + default interceptors lets the server
	// negotiate and receive a media track. Receiving SRTP over the connection is
	// what exercises the DTLS 1.3 SRTP keying-material exporter: without a working
	// 1.3 exporter the handshake still reaches "connected", but SRTP cannot be
	// keyed, so OnTrack never delivers RTP.
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		panic(fmt.Sprintf("register default codecs: %v", err))
	}
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		panic(fmt.Sprintf("register default interceptors: %v", err))
	}

	settingEngine := webrtc.SettingEngine{}

	// Optional verbose logging (DEMO_LOG=debug|trace|warn).
	logLevel := logging.LogLevelInfo
	switch os.Getenv("DEMO_LOG") {
	case "trace":
		logLevel = logging.LogLevelTrace
	case "debug":
		logLevel = logging.LogLevelDebug
	case "warn":
		logLevel = logging.LogLevelWarn
	}
	loggerFactory := logging.NewDefaultLoggerFactory()
	loggerFactory.DefaultLogLevel = logLevel
	settingEngine.LoggerFactory = loggerFactory

	// Request DTLS 1.3. pion/dtls defaults the version range to a 1.2 floor, so
	// 1.3 must be requested explicitly via the SetDTLSVersionRange knob (added by
	// the webrtc patch this demo applies). Leaving min unset (zero Version) lets
	// pion/dtls apply its default floor; max = 1.3 negotiates the highest version
	// both peers support.
	//
	// Control: set DEMO_DTLS_MAX=12 to force a 1.2 ceiling, to compare 1.2 vs 1.3.
	maxVersion := protocol.Version1_3
	if os.Getenv("DEMO_DTLS_MAX") == "12" {
		maxVersion = protocol.Version1_2
	}
	settingEngine.SetDTLSVersionRange(protocol.Version(0), maxVersion)

	return webrtc.NewAPI(
		webrtc.WithSettingEngine(settingEngine),
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)
}

func setupOfferHandler(pc **webrtc.PeerConnection) {
	http.HandleFunc("/offer", func(w http.ResponseWriter, r *http.Request) {
		var offer webrtc.SessionDescription
		if err := json.NewDecoder(r.Body).Decode(&offer); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		api := newAPI()
		var err error
		*pc, err = api.NewPeerConnection(webrtc.Configuration{
			BundlePolicy: webrtc.BundlePolicyMaxBundle,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}

		setupICECandidateHandler(*pc)
		setupDataChannelHandler(*pc)
		// processOffer detects the browser's video codec from the offer, creates
		// a matching loopback track, adds it before answering, and registers the
		// OnTrack handler. No renegotiation needed.
		if err := processOffer(*pc, offer, w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
	})
}

func setupICECandidateHandler(pc *webrtc.PeerConnection) {
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c != nil {
			fmt.Printf("New ICE candidate: %s\n", c.Address)
		}
	})
}

func setupDataChannelHandler(pc *webrtc.PeerConnection) {
	pc.OnDataChannel(func(d *webrtc.DataChannel) {
		d.OnOpen(func() {
			fmt.Println("DataChannel opened (server)")
			if err := d.SendText("Hello from the Go server"); err != nil {
				fmt.Printf("Failed to send text: %v\n", err)
			}
		})
		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			fmt.Printf("Received: %s\n", string(msg.Data))
			if err := d.SendText("ECHO " + string(msg.Data)); err != nil {
				fmt.Printf("Failed to send text: %v\n", err)
			}
		})
	})
}

// setupTrackHandler registers OnTrack: it drains inbound RTP (proving the
// server's SRTP decrypt path over the negotiated DTLS version) AND forwards each
// packet to the loopback track (proving the server's SRTP encrypt path,
// Go->browser). The loopback track was created with the browser's negotiated
// video codec before the answer (see processOffer), so no renegotiation is
// needed. The browser rendering the returned video is the both-directions proof.
func setupTrackHandler(pc *webrtc.PeerConnection, loopback *webrtc.TrackLocalStaticRTP) {
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		codec := track.Codec()
		fmt.Printf("OnTrack: kind=%s codec=%s payloadType=%d ssrc=%d\n",
			track.Kind(), codec.MimeType, track.PayloadType(), track.SSRC())

		echo := loopback != nil && track.Kind() == webrtc.RTPCodecTypeVideo

		go func() {
			var pkts, echoed, bytesRecv uint64
			var echoErrLogged bool
			for {
				pkt, _, err := track.ReadRTP()
				if err != nil {
					if !errors.Is(err, io.EOF) {
						fmt.Printf("   track %s read ended: %v (after %d packets)\n", track.Kind(), err, pkts)
					}

					return
				}
				pkts++
				bytesRecv += uint64(len(pkt.Payload))
				if pkts == 1 {
					fmt.Printf("SRTP media flowing over DTLS (recv): first %s RTP packet decrypted "+
						"(seq=%d ts=%d)\n", track.Kind(), pkt.SequenceNumber, pkt.Timestamp)
				}
				if echo {
					if werr := loopback.WriteRTP(pkt); werr != nil {
						if !echoErrLogged {
							echoErrLogged = true
							fmt.Printf("   loopback WriteRTP error: %v\n", werr)
						}
					} else {
						echoed++
						if echoed == 1 {
							fmt.Printf("SRTP loopback active (send): first %s RTP packet re-sent to browser\n",
								track.Kind())
						}
					}
				}
				if pkts%100 == 0 {
					fmt.Printf("   %s: %d RTP packets recv (%d bytes), %d echoed back\n",
						track.Kind(), pkts, bytesRecv, echoed)
				}
			}
		}()
	})
}

// videoCodecFromOffer inspects the browser's offer and returns an
// RTPCodecCapability for its top-preferred video codec, so the server's loopback
// track can be created with a matching codec (VP8 for Chrome, H.264 for Safari,
// etc.). Returns ok=false if the offer has no video section.
func videoCodecFromOffer(offer webrtc.SessionDescription) (webrtc.RTPCodecCapability, bool) {
	parsed, err := offer.Unmarshal()
	if err != nil {
		return webrtc.RTPCodecCapability{}, false
	}
	for _, m := range parsed.MediaDescriptions {
		if m.MediaName.Media != "video" {
			continue
		}
		// Walk the payload types in offered order; return the first one whose
		// rtpmap names a codec we can map to a Pion MimeType.
		for _, pt := range m.MediaName.Formats {
			for _, a := range m.Attributes {
				if a.Key != "rtpmap" || !strings.HasPrefix(a.Value, pt+" ") {
					continue
				}
				if mime, ok := mimeFromRtpmap(a.Value); ok {
					return webrtc.RTPCodecCapability{MimeType: mime, ClockRate: 90000}, true
				}
			}
		}
	}

	return webrtc.RTPCodecCapability{}, false
}

// mimeFromRtpmap maps an SDP rtpmap value (e.g. "96 VP8/90000") to a Pion video
// MimeType constant. Returns ok=false for non-video or unrecognized codecs.
func mimeFromRtpmap(rtpmap string) (string, bool) {
	upper := strings.ToUpper(rtpmap)
	switch {
	case strings.Contains(upper, " VP8/"):
		return webrtc.MimeTypeVP8, true
	case strings.Contains(upper, " VP9/"):
		return webrtc.MimeTypeVP9, true
	case strings.Contains(upper, " H264/"):
		return webrtc.MimeTypeH264, true
	case strings.Contains(upper, " AV1/"):
		return webrtc.MimeTypeAV1, true
	default:
		return "", false
	}
}

func processOffer(pc *webrtc.PeerConnection, offer webrtc.SessionDescription, w http.ResponseWriter) error {
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}

	// Create a loopback track matching the browser's preferred video codec and
	// add it BEFORE the answer, so the single answer offers the browser a video
	// section it can decode -- no renegotiation needed.
	if codecCap, ok := videoCodecFromOffer(offer); ok {
		loopback, err := webrtc.NewTrackLocalStaticRTP(codecCap, "loopback-video", "dtls13-demo-loopback")
		if err != nil {
			return err
		}
		if _, err = pc.AddTrack(loopback); err != nil {
			return err
		}
		fmt.Printf("Loopback track created with codec %s (matched to browser offer)\n", codecCap.MimeType)
		setupTrackHandler(pc, loopback)
	} else {
		fmt.Println("No video codec in offer; loopback disabled")
		setupTrackHandler(pc, nil)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}

	<-webrtc.GatheringCompletePromise(pc)

	final := pc.LocalDescription()
	if final == nil {
		//nolint:err113
		return fmt.Errorf("local description is nil after ICE gathering")
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(*final); err != nil {
		fmt.Printf("Failed to encode answer: %v\n", err)
	}

	return nil
}

func setupCandidateHandler(pc **webrtc.PeerConnection) {
	http.HandleFunc("/candidate", func(w http.ResponseWriter, r *http.Request) {
		var candidate webrtc.ICECandidateInit
		if err := json.NewDecoder(r.Body).Decode(&candidate); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		if *pc != nil {
			if err := (*pc).AddICECandidate(candidate); err != nil {
				fmt.Println("Failed to add candidate", err)
			}
		}
	})
}

func indexPath() string {
	// Resolve index.html next to the executable first, then fall back to CWD, so
	// the server works regardless of where it is launched from.
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "index.html")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "./index.html"
}

func setupStaticHandler() {
	idx := indexPath()
	fmt.Printf("Serving index.html from: %s\n", idx)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)

			return
		}
		http.ServeFile(w, r, idx)
	})
}
