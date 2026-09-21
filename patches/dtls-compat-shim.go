// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package dtls

import (
	"net"

	cryptosuite "github.com/pion/dtls/v3/pkg/crypto/ciphersuite"
)

// Cross-repo skew shim.
//
// This demo pins pion/dtls to an unreleased main commit (the interop-verified
// DTLS 1.3 base carrying the keying-material exporter fix). That commit renamed
// a few public symbols after pion/stun's last released tag was cut, so released
// pion/stun (pulled transitively by released pion/webrtc) still calls the older
// names. This file re-adds those names as thin aliases so the pinned stack
// compiles. Each alias can be removed once its consumer catches up (or once a
// DTLS 1.3 release lands and consumers pin it).
//
// Bridged renames (pion/dtls main history):
//   - ClientWithOptions / ServerWithOptions -> Client / Server
//   - dtls.CipherSuiteID / dtls.CipherSuite moved to pkg/crypto/ciphersuite

// CipherSuiteID is a compatibility alias for the cipher-suite identifier type,
// relocated to pkg/crypto/ciphersuite.
type CipherSuiteID = cryptosuite.ID

// CipherSuite is a compatibility alias for the cipher-suite interface, relocated
// to pkg/crypto/ciphersuite.
type CipherSuite = cryptosuite.Suite

// ClientWithOptions is a compatibility alias for Client.
func ClientWithOptions(conn net.PacketConn, rAddr net.Addr, opts ...ClientOption) (*Conn, error) {
	return Client(conn, rAddr, opts...)
}

// ServerWithOptions is a compatibility alias for Server.
func ServerWithOptions(conn net.PacketConn, rAddr net.Addr, opts ...ServerOption) (*Conn, error) {
	return Server(conn, rAddr, opts...)
}
