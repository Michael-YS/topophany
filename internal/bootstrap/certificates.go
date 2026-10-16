// Package bootstrap generates reviewed installation bundles. It never connects
// to SSH hosts, downloads programs, or executes a remote topology.
package bootstrap

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"
)

func certificates(hosts []string) (ca, key, cert, serverKey []byte, err error) {
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		err = e
		return
	}
	serial := func() *big.Int {
		n, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if e != nil {
			err = e
		}
		return n
	}
	now := time.Now()
	authority := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Topophany private CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(5, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	if err != nil {
		return
	}
	raw, e := x509.CreateCertificate(rand.Reader, authority, authority, pub, priv)
	if e != nil {
		err = e
		return
	}
	ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	private, e := x509.MarshalPKCS8PrivateKey(priv)
	if e != nil {
		err = e
		return
	}
	key = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	serverPub, serverPriv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		err = e
		return
	}
	leaf := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "Topophany collector"}, NotBefore: authority.NotBefore, NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			leaf.IPAddresses = append(leaf.IPAddresses, ip)
		} else {
			leaf.DNSNames = append(leaf.DNSNames, h)
		}
	}
	if err != nil {
		return
	}
	raw, e = x509.CreateCertificate(rand.Reader, leaf, authority, serverPub, priv)
	if e != nil {
		err = e
		return
	}
	cert = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	private, e = x509.MarshalPKCS8PrivateKey(serverPriv)
	if e != nil {
		err = e
		return
	}
	serverKey = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	return
}

// expires is when the first certificate in a PEM stops being valid; zero
// when there is none to read.
func expires(certPEM []byte) time.Time {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}
	}
	c, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return time.Time{}
	}
	return c.NotAfter
}
