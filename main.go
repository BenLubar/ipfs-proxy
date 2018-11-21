// +build linux

package main

import (
	"flag"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

var flagBaseURL = flag.String("baseurl", "https://ipfs.io", "base IPFS server URL")
var flagBaseDir = flag.String("basedir", "/data", "base directory for files")
var flagWatch = flag.Bool("watch", true, "use -watch=false to disable fanotify support")
var flagBootstrap = flag.String("bootstrap", "", "bootstrap ipfs from files in this directory tree instead of running a proxy")

func main() {
	flag.Parse()

	if *flagBootstrap != "" {
		bootstrap(*flagBootstrap)
		return
	}

	if *flagWatch {
		go watch()
	}

	if err := http.ListenAndServe(":8089", http.HandlerFunc(serveFile)); err != nil {
		panic(err)
	}
}

func serveFile(w http.ResponseWriter, r *http.Request) {
	var hash [1024]byte
	relPath := filepath.Clean(r.URL.Path)
	absPath := filepath.Join(*flagBaseDir, filepath.Clean(r.Host), relPath)
	sz, err := syscall.Getxattr(absPath, "user.ipfs-hash", hash[:])
	if err == syscall.ENOENT {
		notFound(w, r)
		return
	}

	if err == syscall.ENODATA {
		addAndServeFile(w, r, absPath)
		return
	}

	w.Header().Add("Cache-Control", "public, max-age=31536000, immutable")
	http.Redirect(w, r, *flagBaseURL+"/ipfs/"+string(hash[:sz]), http.StatusMovedPermanently)
}

func addAndServeFile(w http.ResponseWriter, r *http.Request, absPath string) {
	fi, err := os.Stat(absPath)
	if err != nil {
		w.Header().Add("Cache-Control", "private, max-age=0, stale-while-revalidate=300")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !fi.Mode().IsRegular() {
		notFound(w, r)
		return
	}

	hash, err := addFileToIPFS(absPath)
	if err != nil {
		w.Header().Add("Cache-Control", "private, max-age=0, stale-while-revalidate=300")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Cache-Control", "public, max-age=31536000, immutable")
	http.Redirect(w, r, *flagBaseURL+"/ipfs/"+hash, http.StatusMovedPermanently)
}

func addFileToIPFS(absPath string) (string, error) {
	cmd := exec.Command("ipfs", "--api", "/dns4/daemon/tcp/5001", "add", "-Q", "--nocopy", "--fscache", "--inline", absPath)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	hash := strings.TrimSpace(string(b))
	if err = syscall.Setxattr(absPath, "user.ipfs-hash", []byte(b), 0); err != nil {
		return "", err
	}
	return hash, nil
}

func notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Cache-Control", "public, max-age=0, stale-while-revalidate=300")
	http.NotFound(w, r)
}
