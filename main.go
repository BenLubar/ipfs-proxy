// +build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"syscall"
)

var flagAPIEndpoint = flag.String("api", "http://daemon:5001", "the IPFS API endpoint")
var flagBaseURL = flag.String("baseurl", "https://gateway.ipfs.io", "base IPFS server URL")
var flagBaseDir = flag.String("basedir", "/data", "base directory for files")
var flagWatch = flag.Bool("watch", true, "use -watch=false to disable fanotify support")
var flagBootstrap = flag.String("bootstrap", "", "bootstrap ipfs from files in this directory tree instead of running a proxy")
var flagCacheHit = flag.String("cache-hit", "public, max-age=31536000, immutable", "cache-control header for success")
var flagCacheMiss = flag.String("cache-miss", "private, max-age=0, stale-while-revalidate=300", "cache-control header for errors")

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

	w.Header().Add("Cache-Control", *flagCacheHit)
	ipfsPath := "/ipfs/" + string(hash[:sz])
	w.Header().Add("X-Ipfs-Path", ipfsPath)
	http.Redirect(w, r, *flagBaseURL+ipfsPath, http.StatusMovedPermanently)
}

func addAndServeFile(w http.ResponseWriter, r *http.Request, absPath string) {
	fi, err := os.Stat(absPath)
	if err != nil {
		w.Header().Add("Cache-Control", *flagCacheMiss)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !fi.Mode().IsRegular() {
		notFound(w, r)
		return
	}

	hash, err := addFileToIPFS(r.Context(), absPath)
	if err != nil {
		w.Header().Add("Cache-Control", *flagCacheMiss)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Cache-Control", *flagCacheHit)
	ipfsPath := "/ipfs/" + hash
	w.Header().Add("X-Ipfs-Path", ipfsPath)
	http.Redirect(w, r, *flagBaseURL+ipfsPath, http.StatusMovedPermanently)
}

func addFileToIPFS(ctx context.Context, absPath string) (string, error) {
	var buf bytes.Buffer
	mimeType, err := writeMultipartBody(&buf, absPath)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", *flagAPIEndpoint+"/api/v0/add?"+url.Values{
		"pin": {"false"},
	}.Encode(), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mimeType)
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	var data struct {
		Hash string
	}
	err = json.NewDecoder(resp.Body).Decode(&data)
	_ = resp.Body.Close()
	if err != nil {
		return "", err
	}

	req, err = http.NewRequest("POST", *flagAPIEndpoint+"/api/v0/files/mkdir?"+url.Values{
		"arg":     {path.Dir(absPath)},
		"parents": {"true"},
		"flush":   {"false"},
	}.Encode(), nil)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()

	req, err = http.NewRequest("POST", *flagAPIEndpoint+"/api/v0/files/cp?"+url.Values{
		"arg": {"/ipfs/" + data.Hash, absPath},
	}.Encode(), nil)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()

	if err = syscall.Setxattr(absPath, "user.ipfs-hash", []byte(data.Hash), 0); err != nil {
		return "", err
	}
	return data.Hash, nil
}

func notFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Cache-Control", *flagCacheMiss)
	http.NotFound(w, r)
}

func writeMultipartBody(w io.Writer, absPath string) (mimeType string, err error) {
	data := multipart.NewWriter(w)
	w, err = data.CreatePart(textproto.MIMEHeader{
		"Abspath":             {absPath},
		"Content-Disposition": {fmt.Sprintf("file; filename=%q", filepath.Base(absPath))},
		"Content-Type":        {"application/octet-stream"},
	})
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
