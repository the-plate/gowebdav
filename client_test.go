package gowebdav

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/webdav"
)

func basicAuth(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if user, passwd, ok := r.BasicAuth(); ok {
			if user == "user" && passwd == "password" {
				h.ServeHTTP(w, r)
				return
			}

			http.Error(w, "not authorized", 403)
		} else {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(401)
		}
	}
}

func fillFs(t *testing.T, fs webdav.FileSystem) context.Context {
	ctx := context.Background()
	f, err := fs.OpenFile(ctx, "hello.txt", os.O_CREATE, 0644)
	if err != nil {
		t.Errorf("fail to crate file: %v", err)
	}
	f.Write([]byte("hello gowebdav\n"))
	f.Close()
	err = fs.Mkdir(ctx, "/test", 0755)
	if err != nil {
		t.Errorf("fail to crate directory: %v", err)
	}
	f, err = fs.OpenFile(ctx, "/test/test.txt", os.O_CREATE, 0644)
	if err != nil {
		t.Errorf("fail to crate file: %v", err)
	}
	f.Write([]byte("test test gowebdav\n"))
	f.Close()
	return ctx
}

func newServer(t *testing.T) (*Client, *httptest.Server, webdav.FileSystem, context.Context) {
	mux := http.NewServeMux()
	fs := webdav.NewMemFS()
	ctx := fillFs(t, fs)
	mux.HandleFunc("/", basicAuth(&webdav.Handler{
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}))
	srv := httptest.NewServer(mux)
	cli := NewClient(srv.URL, "user", "password")
	return cli, srv, fs, ctx
}

func TestConnect(t *testing.T) {
	cli, srv, _, _ := newServer(t)
	defer srv.Close()
	if err := cli.Connect(); err != nil {
		t.Fatalf("got error: %v, want nil", err)
	}

	cli = NewClient(srv.URL, "no", "no")
	if err := cli.Connect(); err == nil {
		t.Fatalf("got nil, want error: %v", err)
	}
}

func TestReadDirConcurrent(t *testing.T) {
	cli, srv, _, ctx := newServer(t)
	defer srv.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			f, err := cli.ReadDir(ctx, "/")
			if err != nil {
				errs <- errors.New(fmt.Sprintf("got error: %v, want file listing: %v", err, f))
			}
			if len(f) != 2 {
				errs <- errors.New(fmt.Sprintf("f: %v err: %v", f, err))
			}
			if f[0].Name() != "hello.txt" && f[1].Name() != "hello.txt" {
				errs <- errors.New(fmt.Sprintf("got: %v, want file: %s", f, "hello.txt"))
			}
			if f[0].Name() != "test" && f[1].Name() != "test" {
				errs <- errors.New(fmt.Sprintf("got: %v, want directory: %s", f, "test"))
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRead(t *testing.T) {
	cli, srv, _, ctx := newServer(t)
	defer srv.Close()

	data, err := cli.Read(ctx, "/hello.txt")
	if err != nil || bytes.Compare(data, []byte("hello gowebdav\n")) != 0 {
		t.Fatalf("got: %v, want data: %s", err, []byte("hello gowebdav\n"))
	}

	data, err = cli.Read(ctx, "/404.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", data, err)
	}
	if !IsErrNotFound(err) {
		t.Fatalf("got: %v, want 404 error", err)
	}
}

func TestReadStream(t *testing.T) {
	cli, srv, _, ctx := newServer(t)
	defer srv.Close()

	stream, err := cli.ReadStream(ctx, "/hello.txt")
	if err != nil {
		t.Fatalf("got: %v, want data: %v", err, stream)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(stream)
	if buf.String() != "hello gowebdav\n" {
		t.Fatalf("got: %v, want stream: hello gowebdav", buf.String())
	}

	stream, err = cli.ReadStream(ctx, "/404/hello.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", stream, err)
	}
}

func TestReadStreamRange(t *testing.T) {
	cli, srv, _, ctx := newServer(t)
	defer srv.Close()

	stream, err := cli.ReadStreamRange(ctx, "/hello.txt", 4, 4)
	if err != nil {
		t.Fatalf("got: %v, want data: %v", err, stream)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(stream)
	if buf.String() != "o go" {
		t.Fatalf("got: %v, want stream: o go", buf.String())
	}

	stream, err = cli.ReadStream(ctx, "/404/hello.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", stream, err)
	}
}

func TestReadStreamRangeUnkownLength(t *testing.T) {
	cli, srv, _, ctx := newServer(t)
	defer srv.Close()

	stream, err := cli.ReadStreamRange(ctx, "/hello.txt", 6, 0)
	if err != nil {
		t.Fatalf("got: %v, want data: %v", err, stream)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(stream)
	if buf.String() != "gowebdav\n" {
		t.Fatalf("got: %v, want stream: gowebdav\n", buf.String())
	}

	stream, err = cli.ReadStream(ctx, "/404/hello.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", stream, err)
	}
}

func TestStat(t *testing.T) {
	cli, srv, _, ctx := newServer(t)
	defer srv.Close()

	info, err := cli.Stat(ctx, "/hello.txt")
	if err != nil {
		t.Fatalf("got: %v, want os.Info: %v", err, info)
	}
	if info.Name() != "hello.txt" {
		t.Fatalf("got: %v, want file hello.txt", info)
	}

	info, err = cli.Stat(ctx, "/404.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}
	if !IsErrNotFound(err) {
		t.Fatalf("got: %v, want 404 error", err)
	}
}

func TestMkdir(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	info, err := cli.Stat(ctx, "/newdir")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}

	if err := cli.Mkdir(ctx, "/newdir", 0755); err != nil {
		t.Fatalf("got: %v, want mkdir /newdir", err)
	}

	if err := cli.Mkdir(ctx, "/newdir", 0755); err != nil {
		t.Fatalf("got: %v, want mkdir /newdir", err)
	}

	info, err = fs.Stat(ctx, "/newdir")
	if err != nil {
		t.Fatalf("got: %v, want dir info: %v", err, info)
	}

	if err := cli.Mkdir(ctx, "/404/newdir", 0755); err == nil {
		t.Fatalf("expected Mkdir error due to missing parent directory")
	}
}

func TestMkdirAll(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	if err := cli.MkdirAll(ctx, "/dir/dir/dir", 0755); err != nil {
		t.Fatalf("got: %v, want mkdirAll /dir/dir/dir", err)
	}

	info, err := fs.Stat(ctx, "/dir/dir/dir")
	if err != nil {
		t.Fatalf("got: %v, want dir info: %v", err, info)
	}
}

func TestCopy(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	info, err := fs.Stat(ctx, "/copy.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}

	if err := cli.Copy(ctx, "/hello.txt", "/copy.txt", false); err != nil {
		t.Fatalf("got: %v, want copy /hello.txt to /copy.txt", err)
	}

	info, err = fs.Stat(ctx, "/copy.txt")
	if err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
	if info.Size() != 15 {
		t.Fatalf("got: %v, want file size: %d bytes", info.Size(), 15)
	}

	info, err = fs.Stat(ctx, "/hello.txt")
	if err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
	if info.Size() != 15 {
		t.Fatalf("got: %v, want file size: %d bytes", info.Size(), 15)
	}

	if err := cli.Copy(ctx, "/hello.txt", "/copy.txt", false); err == nil {
		t.Fatalf("expected copy error due to overwrite false")
	}

	if err := cli.Copy(ctx, "/hello.txt", "/copy.txt", true); err != nil {
		t.Fatalf("got: %v, want overwrite /copy.txt with /hello.txt", err)
	}
}

func TestRename(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	info, err := fs.Stat(ctx, "/copy.txt")
	if err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}

	if err := cli.Rename(ctx, "/hello.txt", "/copy.txt", false); err != nil {
		t.Fatalf("got: %v, want mv /hello.txt to /copy.txt", err)
	}

	info, err = fs.Stat(ctx, "/copy.txt")
	if err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
	if info.Size() != 15 {
		t.Fatalf("got: %v, want file size: %d bytes", info.Size(), 15)
	}

	if info, err = fs.Stat(ctx, "/hello.txt"); err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}

	if err := cli.Rename(ctx, "/test/test.txt", "/copy.txt", true); err != nil {
		t.Fatalf("got: %v, want overwrite /copy.txt with /hello.txt", err)
	}
	info, err = fs.Stat(ctx, "/copy.txt")
	if err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
	if info.Size() != 19 {
		t.Fatalf("got: %v, want file size: %d bytes", info.Size(), 19)
	}
}

func TestRemove(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	if err := cli.Remove(ctx, "/hello.txt"); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}

	if info, err := fs.Stat(ctx, "/hello.txt"); err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}

	if err := cli.Remove(ctx, "/404.txt"); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}
}

func TestRemoveAll(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	if err := cli.RemoveAll(ctx, "/test/test.txt"); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}

	if info, err := fs.Stat(ctx, "/test/test.txt"); err == nil {
		t.Fatalf("got: %v, want error: %v", info, err)
	}

	if err := cli.RemoveAll(ctx, "/404.txt"); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}

	if err := cli.RemoveAll(ctx, "/404/404/404.txt"); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}
}

func TestWrite(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	if err := cli.Write(ctx, "/newfile.txt", []byte("foo bar\n"), 0660); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}

	info, err := fs.Stat(ctx, "/newfile.txt")
	if err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
	if info.Size() != 8 {
		t.Fatalf("got: %v, want file size: %d bytes", info.Size(), 8)
	}

	if err := cli.Write(ctx, "/404/newfile.txt", []byte("foo bar\n"), 0660); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}
}

func TestWriteStream(t *testing.T) {
	cli, srv, fs, ctx := newServer(t)
	defer srv.Close()

	if err := cli.WriteStream(ctx, "/newfile.txt", strings.NewReader("foo bar\n"), 0660); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}

	info, err := fs.Stat(ctx, "/newfile.txt")
	if err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
	if info.Size() != 8 {
		t.Fatalf("got: %v, want file size: %d bytes", info.Size(), 8)
	}

	if err := cli.WriteStream(ctx, "/404/works.txt", strings.NewReader("foo bar\n"), 0660); err != nil {
		t.Fatalf("got: %v, want nil", err)
	}

	if info, err := fs.Stat(ctx, "/404/works.txt"); err != nil {
		t.Fatalf("got: %v, want file info: %v", err, info)
	}
}
