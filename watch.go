// +build linux

package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"bitbucket.org/madmo/fanotify"
)

func watch() {
	notify, err := fanotify.Initialize(0, os.O_RDONLY)
	if err != nil {
		panic(err)
	}

	if err = notify.Mark(fanotify.FAN_MARK_ADD|fanotify.FAN_MARK_MOUNT, fanotify.FAN_CLOSE_WRITE, 0, "/data/"); err != nil {
		panic(err)
	}

	for {
		ev, err := notify.GetEvent()
		if err != nil {
			log.Println("fanotify:", err)
			continue
		}

		name := ev.File.Name()
		err = ev.File.Close()
		if err != nil {
			log.Println("fanotify:", err)
			continue
		}

		if strings.HasPrefix(name, "/data/") && !strings.HasPrefix(name, "/data/ipfs/") {
			go addFileToIPFS(context.TODO(), name)
		}
	}
}

func bootstrap(root string) {
	var wg sync.WaitGroup
	wg.Add(runtime.GOMAXPROCS(0))
	ch := make(chan string, 100)
	for i := 0; i < runtime.GOMAXPROCS(0); i++ {
		go func() {
			defer wg.Done()
			for path := range ch {
				hash, err := addFileToIPFS(context.TODO(), path)
				log.Printf("bootstrapping %q: %v %v", path, hash, err)
			}
		}()
	}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("error in %q: %v", path, err)
			return err
		}
		if path == "/data/ipfs" || strings.HasPrefix(path, "/data/ipfs/") {
			return filepath.SkipDir
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		ch <- path
		return nil
	})

	close(ch)
	wg.Wait()
}
