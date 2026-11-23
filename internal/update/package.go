package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const MaxPackage = 256 << 20

var (
	PackageName = regexp.MustCompile(`^Sailfin-update-[a-f0-9]{32}\.bin$`)
	ID          = regexp.MustCompile(`^upload-[0-9]+$`)
	signature   = regexp.MustCompile(`(?m)^# sailfin-signature: ([a-f0-9]{128})$`)
)

func Dir(db string) string {
	return filepath.Join(filepath.Dir(db), ".sailfin-updates")
}

func PublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("更新公钥必须使用 PEM 格式")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	public, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("更新公钥必须使用 Ed25519")
	}
	return public, nil
}

func Verify(r io.Reader, key ed25519.PublicKey, platform string) error {
	if len(key) != ed25519.PublicKeySize || platform == "" {
		return errors.New("更新公钥或目标平台未配置")
	}
	reader := bufio.NewReader(r)
	header, err := reader.Peek(512)
	if err != nil || !bytes.HasPrefix(header, []byte("#!/bin/bash\n")) ||
		!bytes.Contains(header, []byte("\npackage_kind='update'\n")) ||
		!bytes.Contains(header, []byte("\nplatform='"+platform+"'\n")) {
		return errors.New("文件不是当前平台的 Sailfin 更新包")
	}
	match := signature.FindSubmatchIndex(header)
	if len(match) != 4 {
		return errors.New("更新包缺少发布签名")
	}
	sig, _ := hex.DecodeString(string(header[match[2]:match[3]]))
	hash := sha256.New()
	_, _ = hash.Write(header[:match[2]])
	_, _ = hash.Write(bytes.Repeat([]byte{'0'}, 128))
	if _, err := reader.Discard(match[3]); err != nil {
		return err
	}
	if _, err := io.Copy(hash, reader); err != nil {
		return err
	}
	if !ed25519.Verify(key, hash.Sum(nil), sig) {
		return errors.New("更新包签名校验失败")
	}
	return nil
}
