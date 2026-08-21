// Command release packages a built executable into the archive that gets
// attached to a GitHub release.
//
// It exists so the archive is produced the same way every time: the same files,
// the same name, and a failure if the executable has not been built rather than
// a zip that quietly ships without it.
//
//	wails3 build
//	go run ./cmd/release
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MuhibAhmed/openvpn-desktop/internal/version"
)

func main() {
	binary := flag.String("binary", filepath.Join("bin", "openvpn-desktop.exe"), "the built executable to package")
	outDir := flag.String("out", "dist", "directory to write the archive to")
	v := flag.String("version", version.Current, "version to name the archive after")
	flag.Parse()

	// The documentation travels with the binary: someone who downloads the zip
	// and never sees the repository still needs the licence and the notes on
	// what to install first.
	extras := []string{"README.md", "LICENSE", "CHANGELOG.md"}

	name := fmt.Sprintf("vpn-desktop-%s-windows-amd64.zip", *v)
	path := filepath.Join(*outDir, name)

	if err := run(*binary, extras, path); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}

	info, _ := os.Stat(path)
	fmt.Printf("wrote %s (%.1f MB)\n", path, float64(info.Size())/(1<<20))
}

func run(binary string, extras []string, out string) error {
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("%s is missing; run \"wails3 build\" first", binary)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, src := range append([]string{binary}, extras...) {
		if err := add(zw, src); err != nil {
			// Close before returning so the half-written archive is not left
			// looking like a usable one.
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

// add copies one file into the archive under its base name, so the zip extracts
// as a flat folder rather than reproducing bin/ and docs/.
func add(zw *zip.Writer, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.Base(src)
	header.Method = zip.Deflate

	w, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, in)
	return err
}
