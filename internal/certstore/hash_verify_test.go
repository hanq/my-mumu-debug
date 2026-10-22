package certstore

import (
	"crypto/md5"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 对拍: 实机 openssl 算出的 c8750f0d
func TestSubjectHashOld(t *testing.T) {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".mitmproxy", "mitmproxy-ca-cert.pem"))
	if err != nil {
		t.Skip("CA 不存在, 跳过")
	}
	block, _ := pem.Decode(data)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(cert.RawSubject)
	got := fmt.Sprintf("%08x", binary.LittleEndian.Uint32(sum[:4]))
	if got != "c8750f0d" {
		t.Fatalf("hash 不匹配: got=%s want=c8750f0d", got)
	}
	t.Logf("OK: %s == c8750f0d", got)
}
