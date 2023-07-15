package hashfs

import (
	"embed"
	"net/http"
	"net/http/httptest"
	"testing"
)

//go:embed testdata
var fsys embed.FS

// Hashes for testdata files.
const (
	scriptjs     = "e959523c7cd6350c847a50ba64d1876900e1ee9dcf3b6c4abb8a6b8e6c13b262"
	stylesmincss = "c36dd06b311aa3f26ebe91cae8607a18b4a4de23f6d1c0c40943afbf07b8278d"
	indexhtml    = "b633a587c652d02386c4f16f8c6f6aab7352d97f16367c3c40576214372dd628"
	texttxt      = "810ff2fb242a5dee4220f2cb0e6a519891fb67f2f828a6cab4ef8894633b1f50"
)

func TestSetHashLocation(t *testing.T) {
	t.Run("Start", func(t *testing.T) {
		hfs := NewFS(fsys, HashLocationStart())
		if hfs.hashLocation != hashLocationStart {
			t.Fatal("Error setting hash location to start")
		}
	})
	t.Run("End", func(t *testing.T) {
		hfs := NewFS(fsys, HashLocationEnd())
		if hfs.hashLocation != hashLocationEnd {
			t.Fatal("Error setting hash location to end")
		}
	})
	t.Run("FirstPeriod", func(t *testing.T) {
		hfs := NewFS(fsys, HashLocationFirstPeriod())
		if hfs.hashLocation != hashLocationFirstPeriod {
			t.Fatal("Error setting hash location to first period")
		}
	})
}

func TestGetHashPath(t *testing.T) {
	t.Run("Start", func(t *testing.T) {
		hfs := NewFS(fsys, HashLocationStart())

		originalPath := "testdata/subdir1/script.js"
		expectedPath := "testdata/subdir1/" + scriptjs + "-script.js"
		hashPath := hfs.GetHashPath(originalPath)
		if hashPath == originalPath {
			t.Fatal("hash path not calculated, original returned")
			return
		}
		if hashPath != expectedPath {
			t.Fatal("hash not added to filename correctly", hashPath)
		}
	})

	t.Run("End", func(t *testing.T) {
		hfs := NewFS(fsys, HashLocationEnd())

		originalPath := "testdata/subdir1/script.js"
		expectedPath := "testdata/subdir1/script.js-" + scriptjs + ".js"
		hashPath := hfs.GetHashPath(originalPath)
		if hashPath == originalPath {
			t.Fatal("hash path not calculated, original returned")
			return
		}
		if hashPath != expectedPath {
			t.Fatal("hash not added to filename correctly", hashPath)
		}
	})

	t.Run("FirstPeriod", func(t *testing.T) {
		hfs := NewFS(fsys, HashLocationFirstPeriod())

		//Filename only has one period, no periods in directory path.
		originalPath := "testdata/subdir1/script.js"
		expectedPath := "testdata/subdir1/script-" + scriptjs + ".js"
		hashPath := hfs.GetHashPath(originalPath)
		if hashPath == originalPath {
			t.Fatal("hash path not calculated, original returned")
			return
		}
		if hashPath != expectedPath {
			t.Fatal("hash not added to filename correctly", hashPath)
		}

		//Filename has more than one period, no periods in directory path.
		originalPath = "testdata/subdir1/styles.min.css"
		hashPath = hfs.GetHashPath(originalPath)
		expectedPath = "testdata/subdir1/styles-" + stylesmincss + ".min.css"
		if hashPath == originalPath {
			return
		}
		if hashPath != expectedPath {
			t.Fatalf("hash not added to filename correctly; \ngot  %s, \nwant %s", hashPath, expectedPath)
		}

		//Filename does not have any periods.
		originalPath = "testdata/subdir1/indexhtml"
		hashPath = hfs.GetHashPath(originalPath)
		expectedPath = "testdata/subdir1/indexhtml-" + indexhtml
		if hashPath == originalPath {
			return
		}
		if hashPath != expectedPath {
			t.Fatalf("hash not added to filename correctly; \ngot  %s, \nwant %s", hashPath, expectedPath)
		}

		//Directory path has period.
		originalPath = "testdata/sub.dir.2/text.txt"
		hashPath = hfs.GetHashPath(originalPath)
		expectedPath = "testdata/sub.dir.2/text-" + texttxt + ".txt"
		if hashPath == originalPath {
			return
		}
		if hashPath != expectedPath {
			t.Fatalf("hash not added to filename correctly; \ngot  %s, \nwant %s", hashPath, expectedPath)
		}
	})

	t.Run("RetrieveMoreThanOnce", func(t *testing.T) {
		hfs := NewFS(fsys)

		originalPath := "testdata/subdir1/script.js"
		expectedPath := "testdata/subdir1/script.js-" + scriptjs + ".js"
		hashPath := hfs.GetHashPath(originalPath)
		if hashPath == originalPath {
			t.Fatal("hash path not calculated, original returned")
			return
		}
		if hashPath != expectedPath {
			t.Fatal("hash not ad∏ded to filename correctly", hashPath)
		}

		//Retrieve the same hash name again to check we can get it from the
		//lookup table.
		hashPath = hfs.GetHashPath(originalPath)
		if hashPath == originalPath {
			t.Fatal("hash path not calculated, original returned")
			return
		}
		if hashPath != expectedPath {
			t.Fatal("hash not added to filename correctly", hashPath)
		}
	})

	t.Run("NonExistantFile", func(t *testing.T) {
		hfs := NewFS(fsys)

		originalPath := "testdata/subdir100/script.js"
		hashPath := hfs.GetHashPath(originalPath)
		if hashPath != originalPath {
			t.Fatal("hash path not calculated, expected original")
			return
		}
	})
}

func TestAddHashToFilename(t *testing.T) {
	hfs := NewFS(fsys)

	hashName := hfs.addHashToFilname("", "")
	if hashName != "" {
		t.Fatal("expected '' because of missing originalName")
		return
	}
	hashName = hfs.addHashToFilname("original.txt", "")
	if hashName != "" {
		t.Fatal("expected '' because of missing hash")
		return
	}

	//Test impossible default case.
	hfs.hashLocation = 400
	hashName = hfs.addHashToFilname("original.txt", "a1b2c3d4")
	if hashName != "" {
		t.Fatal("expected '' because of impossible hash location")
		return
	}
}

func TestOpen(t *testing.T) {
	hfs := NewFS(fsys)
	originalPath := "testdata/sub.dir.2/text.txt"

	t.Run("OriginalName", func(t *testing.T) {
		f, err := hfs.Open(originalPath)
		if err != nil {
			t.Fatal(err)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
			return
		}

		got := make([]byte, info.Size())
		want := "testdata"
		_, err = f.Read(got)
		if err != nil {
			t.Fatal(err)
			return
		}
		if string(got) != want {
			t.Fatalf("did not read file correctly; \ngot:  %s, \nwant: %s", string(got), want)
			return
		}
	})

	t.Run("HashName", func(t *testing.T) {
		hashPath := hfs.GetHashPath(originalPath)

		f, err := hfs.Open(hashPath)
		if err != nil {
			t.Fatal(err)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
			return
		}

		got := make([]byte, info.Size())
		want := "testdata"
		_, err = f.Read(got)
		if err != nil {
			t.Fatal(err)
			return
		}
		if string(got) != want {
			t.Fatalf("did not read file correctly; \ngot:  %s, \nwant: %s", string(got), want)
			return
		}
	})
}

func TestFileServer(t *testing.T) {
	hfs := NewFS(fsys)
	originalPath := "testdata/sub.dir.2/text.txt"

	t.Run("OriginalName", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/"+originalPath, nil)
		w := httptest.NewRecorder()
		s := FileServer(hfs)
		s.ServeHTTP(w, r)

		res := w.Result()
		if res.StatusCode != http.StatusOK {
			t.Fatal("bad code", res.StatusCode)
			return
		}

		got := make([]byte, res.ContentLength)
		want := "testdata"
		_, err := res.Body.Read(got)
		if err != nil {
			t.Fatal(err)
			return
		}
		if string(got) != want {
			t.Fatalf("bad content; \ngot:  %s, \nwant: %s", string(got), want)
			return
		}
	})

	t.Run("HashName", func(t *testing.T) {
		hashPath := hfs.GetHashPath(originalPath)

		r := httptest.NewRequest("GET", "/"+hashPath, nil)
		w := httptest.NewRecorder()
		s := FileServer(hfs)
		s.ServeHTTP(w, r)

		res := w.Result()
		if res.StatusCode != http.StatusOK {
			t.Fatal("bad code", res.StatusCode)
			return
		}

		gotb := make([]byte, res.ContentLength)
		want := "testdata"
		_, err := res.Body.Read(gotb)
		if err != nil {
			t.Fatal(err)
			return
		}
		if string(gotb) != want {
			t.Fatalf("bad content; \ngot:  %s, \nwant: %s", string(gotb), want)
			return
		}

		got := res.Header.Get("Cache-Control")
		want = hfs.getCacheControl()
		if got != want {
			t.Fatalf("bad cache-control; \ngot:  %s, \nwant: %s", string(got), want)
			return
		}

		got = res.Header.Get("Etag")
		rev := hfs.hashPathReverse[hashPath]
		want = rev.hash
		if got != want {
			t.Fatalf("bad etag; \ngot:  %s, \nwant: %s", string(got), want)
			return
		}
	})

	t.Run("FileDoesNotExist", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/badpath.txt", nil)
		w := httptest.NewRecorder()
		s := FileServer(hfs)
		s.ServeHTTP(w, r)

		res := w.Result()
		if res.StatusCode != http.StatusNotFound {
			t.Fatal("bad code", res.StatusCode)
			return
		}
	})

	t.Run("BrowseToDirectory", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/testdata/", nil)
		w := httptest.NewRecorder()
		s := FileServer(hfs)
		s.ServeHTTP(w, r)

		res := w.Result()
		if res.StatusCode != http.StatusForbidden {
			t.Fatal("bad code", res.StatusCode)
			return
		}
	})

	t.Run("BrowseToRootDirectory", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		s := FileServer(hfs)
		s.ServeHTTP(w, r)

		res := w.Result()
		if res.StatusCode != http.StatusForbidden {
			t.Fatal("bad code", res.StatusCode)
			return
		}
	})
}

// func TestPrint(t *testing.T) {
// 	hfs := NewFS(fsys, HashLocationStart())

// 	z := hfs.PrintEmbeddedFileList()
// 	t.Log("Lines:", z, len(z))
// 	t.Fail()
// }
