package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// EnsureDependencies performs all pre-flight checks and self-installs
// any missing pieces (system packages + TTS model files) so the server
// can boot on a fresh machine with one command.
//
// Order:
//  1. tar + bzip2 (needed to extract the model archive)
//  2. audio player binary (aplay / paplay / ffplay)
//  3. TTS model files
func EnsureDependencies(modelDir, playerName string, autoInstall bool) error {
	log.Printf("=== pre-flight check ===")

	if err := ensureBinary("tar", "tar", autoInstall); err != nil {
		return err
	}
	if err := ensureBinary("bzip2", "bzip2", autoInstall); err != nil {
		return err
	}

	playerBin := strings.Fields(playerName)[0]
	pkg := pkgForPlayer(playerBin)
	if pkg == "" {
		log.Printf("custom player %q — skip apt check", playerName)
		if _, err := exec.LookPath(playerBin); err != nil {
			return fmt.Errorf("player binary %s not found in PATH", playerBin)
		}
	} else if err := ensureBinary(playerBin, pkg, autoInstall); err != nil {
		return err
	}

	if err := ensureModel(modelDir); err != nil {
		return err
	}

	log.Printf("=== pre-flight ok ===")
	return nil
}

func pkgForPlayer(bin string) string {
	switch bin {
	case "aplay":
		return "alsa-utils"
	case "paplay":
		return "pulseaudio-utils"
	case "ffplay":
		return "ffmpeg"
	}
	return ""
}

func ensureBinary(bin, pkg string, autoInstall bool) error {
	if _, err := exec.LookPath(bin); err == nil {
		log.Printf("[ok] %s", bin)
		return nil
	}
	log.Printf("[missing] %s — package %q", bin, pkg)

	if !autoInstall {
		return fmt.Errorf("%s missing; install %s manually or run with -auto-install", bin, pkg)
	}
	if err := aptInstall(pkg); err != nil {
		return fmt.Errorf("apt install %s: %w", pkg, err)
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%s still missing after apt install %s", bin, pkg)
	}
	log.Printf("[installed] %s", bin)
	return nil
}

// aptInstall picks whichever package manager exists on the host
// (apt / dnf / yum / pacman / apk / opkg / zypper) and installs the
// distro-appropriate package name when needed.
//
// On Yocto (Genio) opkg may exist but the feeds are usually empty —
// in that case we surface a clear error so the user knows it's a manual fix.
func aptInstall(pkg string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("auto-install only supported on Linux (current: %s)", runtime.GOOS)
	}

	type pm struct {
		bin  string
		args func(pkg string) []string
	}
	pms := []pm{
		{"apt-get", func(p string) []string { return []string{"apt-get", "install", "-y", "--no-install-recommends", p} }},
		{"dnf", func(p string) []string { return []string{"dnf", "install", "-y", p} }},
		{"yum", func(p string) []string { return []string{"yum", "install", "-y", p} }},
		{"pacman", func(p string) []string { return []string{"pacman", "-Sy", "--noconfirm", p} }},
		{"apk", func(p string) []string { return []string{"apk", "add", "--no-cache", p} }},
		{"opkg", func(p string) []string { return []string{"opkg", "install", p} }},
		{"zypper", func(p string) []string { return []string{"zypper", "-n", "install", p} }},
	}

	for _, m := range pms {
		if _, err := exec.LookPath(m.bin); err != nil {
			continue
		}
		args := m.args(pkg)
		if os.Geteuid() != 0 {
			if _, err := exec.LookPath("sudo"); err != nil {
				return fmt.Errorf("not root and no sudo — run as root or install %s manually", pkg)
			}
			args = append([]string{"sudo", "-n"}, args...)
		}
		log.Printf("running: %s", strings.Join(args, " "))
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("no supported package manager found (apt/dnf/yum/pacman/apk/opkg/zypper) — install '%s' manually", pkg)
}

func ensureModel(modelDir string) error {
	if hasModel(modelDir) {
		log.Printf("[ok] model present at %s", modelDir)
		return nil
	}
	log.Printf("[missing] model %s — downloading", modelDir)
	return downloadAndExtractModel(modelDir)
}

func hasModel(modelDir string) bool {
	matches, _ := filepath.Glob(filepath.Join(modelDir, "*.onnx"))
	return len(matches) > 0
}

// downloadAndExtractModel downloads the Sherpa-ONNX archive named after
// the base name of modelDir and extracts it next to modelDir.
//
// Example: modelDir = "./models/vits-zh-hf-fanchen-C"
//   → archive: vits-zh-hf-fanchen-C.tar.bz2
//   → URL:     https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/<archive>
//   → extract under ./models/ producing ./models/vits-zh-hf-fanchen-C/
func downloadAndExtractModel(modelDir string) error {
	parent := filepath.Dir(modelDir)
	base := filepath.Base(modelDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	archiveName := base + ".tar.bz2"
	url := "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/" + archiveName
	archivePath := filepath.Join(parent, archiveName)

	log.Printf("downloading %s", url)
	if err := downloadFile(url, archivePath); err != nil {
		_ = os.Remove(archivePath)
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(archivePath)

	log.Printf("extracting %s into %s", archiveName, parent)
	cmd := exec.Command("tar", "xjf", archivePath, "-C", parent)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar xjf: %w", err)
	}

	if !hasModel(modelDir) {
		return fmt.Errorf("after extraction, no .onnx in %s — archive layout unexpected", modelDir)
	}
	log.Printf("[installed] model at %s", modelDir)
	return nil
}

func downloadFile(url, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	pr := &progressReader{r: resp.Body, total: resp.ContentLength, lastTick: time.Now()}
	if _, err := io.Copy(f, pr); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

type progressReader struct {
	r        io.Reader
	total    int64
	read     int64
	lastTick time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if time.Since(p.lastTick) > 500*time.Millisecond || err == io.EOF {
		mb := float64(p.read) / 1024 / 1024
		if p.total > 0 {
			pct := float64(p.read) * 100 / float64(p.total)
			fmt.Printf("\r  %6.1f MB / %.1f MB  (%5.1f%%)", mb, float64(p.total)/1024/1024, pct)
		} else {
			fmt.Printf("\r  %6.1f MB", mb)
		}
		p.lastTick = time.Now()
	}
	return n, err
}
