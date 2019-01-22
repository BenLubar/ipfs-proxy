// +build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pkg/errors"
)

var flagAPIEndpoint = flag.String("api", "http://daemon:5001", "the IPFS API endpoint")
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

	hash, err := addFileToIPFS(r.Context(), absPath)
	if err != nil {
		w.Header().Add("Cache-Control", "private, max-age=0, stale-while-revalidate=300")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Cache-Control", "public, max-age=31536000, immutable")
	http.Redirect(w, r, *flagBaseURL+"/ipfs/"+hash, http.StatusMovedPermanently)
}

func addFileToIPFS(ctx context.Context, absPath string) (string, error) {
	var buf bytes.Buffer
	mimeType, err := writeMultipartBody(&buf, absPath)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", *flagAPIEndpoint+"/api/v0/add?"+url.Values{
		"arg":     {absPath},
		"quieter": {"true"},
	}.Encode(), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mimeType)
	ctx2, cancel := context.WithCancel(req.Context())
	defer cancel()
	req = req.WithContext(ctx2)

	var data struct {
		Hash string
	}

	errch := make(chan error, 2)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errch <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			b, _ := ioutil.ReadAll(resp.Body)
			errch <- errors.Errorf("%s: %q", resp.Status, b)
			return
		}

		err = json.NewDecoder(resp.Body).Decode(&data)
		errch <- err
	}()

	select {
	case err = <-errch:
	case <-ctx.Done():
		cancel()
		err = ctx.Err()
	}

	if err != nil {
		return "", err
	}

	if err = syscall.Setxattr(absPath, "user.ipfs-hash", []byte(data.Hash), 0); err != nil {
		return "", err
	}
	return data.Hash, nil
}

func notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Cache-Control", "public, max-age=0, stale-while-revalidate=300")
	http.NotFound(w, r)
}

func writeMultipartBody(w io.Writer, absPath string) (mimeType string, err error) {
	data := multipart.NewWriter(w)
	w, err = data.CreateFormFile("file", absPath)
	if err != nil {
		return
	}
	f, err := os.Open(absPath)
	if err != nil {
		return
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()

	_, err = io.Copy(w, f)
	if err != nil {
		return
	}

	return data.FormDataContentType(), data.Close()
}
