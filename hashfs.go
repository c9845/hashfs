package hashfs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Ensure file system implements interface.
var _ fs.FS = (*FS)(nil)

// FS represents an fs.FS file system that can optionally use content addressable
// hashes in the filename. This allows the caller to aggressively cache the
// data since the filename will change if the data changes.
type FS struct {
	fsys fs.FS
	mu   sync.RWMutex

	//Lookup maps.
	pathToHashPath map[string]string    // lookup (path to hash path)
	r              map[string][2]string // reverse lookup (hash path to path)

	//Options.
	hashLocation hashLocation
}

// hashLocation defines the position of the hash in the filename.
type hashLocation int

const (
	hashLocationStart       hashLocation = iota //script.min.js -> a1b2c3...d4e5f6.script.min.js
	hashLocationFirstPeriod                     //script.min.js -> script-a1b2c3...d4e5f6.min.js; original designed hash location
	hashLocationEnd                             //script.min.js -> script.min.a1b2c3...d4e5f6.js

	//default is "at first period in filename" since that was the legacy location of
	//the hash.
	hashLocationDefault = hashLocationFirstPeriod
)

// optionFunc is a function used to modify the way the FS works. For example, setting
// the location of the hash in the filename.
type optionFunc func(*FS)

// NewFS returns an FS for working with hashed static files. Options modify the way
// the FS works when hashing or serving files.
func NewFS(fsys fs.FS, options ...optionFunc) *FS {
	f := &FS{
		fsys: fsys,
		m:    make(map[string]string),
		r:    make(map[string][2]string),
		hl:   hashLocationDefault,
	}

	//Apply any options.
	for _, option := range options {
		option(f)
	}

	return f
}

// HashedPath returns the path to a file with the hash of the file added to the
// file's filename. The hash of the file is calculated here and cached for the
// future.
//
// If a file doesn't exist at the filepath, an error is returned.
func (hf *FS) HashedPath(filepath string) (filepathWithHash string, err error) {
	//Check if the file's hash has already been calculated and cached. This
	//alleviates us from having to calculate the hash each time a file is requested.
	hf.mu.RLock()
	p, exists := hf.pathToHashPath[filepath]
	if exists {
		filepathWithHash = p
		return
	}

	//Look up the file at the filepath to make sure it exists and, if it does, so
	//we can calculate the hash.
	b, err := fs.ReadFile(hf.fsys, filepath)
	if err != nil {
		return
	}

	//Calculate the hash.
	//TODO: support other hash functions? S3 uses MD5?
	hash := sha256.Sum256(b)

	//Encode the hash.
	//TODO: support other encodings?
	encoded := hex.EncodeToString(hash[:])

	//Format the filename with the hash.

}

func (hf *FS) formatFilepathWithHash(filepath, hash string) (filepathWithHash string) {
	//Quick validation.
	if filepath == "" {
		return
	}
	if hash == "" {
		return
	}

	//Split the filepath into path and filename components. We need the filename
	//separately since that is where the hash is added to.
	dir, filename := path.Split(filepath)

	//Add the hash to the filename.
	switch hf.hashLocation {
	case hashLocationFirstPeriod:
		//Handle if the filename doesn't have a period in it. This shouldn't really
		//ever occur since a filename should have an extension. In this case, just
		//append the hash to the filename, separating it with a dash to make it
		//stand out a bit.
		i := strings.Index(filename, ".")
		if i == -1 {
			filename = dir + "-" + hash
			break
		}

		//Add the hash just before the first period, separating it with a dash to
		//make it stand out a bit.
		filename = filename[:i] + "-" + hash + filename[i:]

	case hashLocationStart:
		//Add the hash to the beginning of the filename, separating it with a dash
		//to make it stand out a bit.
		filename = hash + "-" + filename

	case hashLocationEnd:
		//Add the hash to the end of the filename, duplicating the file's extension
		//after the hash to prevent breaking MIME type determination in browsers.
		//The filename and hash is separated with a dash to make it stand out a bit
		//but the extension is copied/appended with a period since that obviously
		//makes sense.
		//
		//Note, path.Ext() returns a value starting with a period (i.e.: .css).
		filename = filename + "-" + hash + path.Ext(filename)

	default:
		//This should never occur since fsys.hashLocation is set by default and
		//can only be set to one of our defined values. This is just here since all
		//switches should have a default.
		filename = ""
	}

	filepathWithHash = path.Join(dir, filename)
	return
}

//
//
//
//
//
//
//

// Open returns a reference to the named file.
// If name is a hash name then the underlying file is used.
func (fsys *FS) Open(name string) (fs.File, error) {
	f, _, err := fsys.open(name)
	return f, err
}

// open
func (fsys *FS) open(name string) (_ fs.File, hash string, err error) {
	// Parse filename to see if it contains a hash.
	// If so, check if hash name matches.
	base, hash := fsys.ParseName(name)
	if hash != "" && fsys.HashName(base) == name {
		name = base
	}

	f, err := fsys.fsys.Open(name)
	return f, hash, err
}

// HashName returns the hash name for a path, if exists.
// Otherwise returns the original path.
func (fsys *FS) HashName(name string) string {
	// Lookup cached formatted name, if exists.
	fsys.mu.RLock()
	if s := fsys.m[name]; s != "" {
		fsys.mu.RUnlock()
		return s
	}
	fsys.mu.RUnlock()

	// Read file contents. Return original filename if we receive an error.
	buf, err := fs.ReadFile(fsys.fsys, name)
	if err != nil {
		return name
	}

	// Compute hash and build filename.
	hash := sha256.Sum256(buf)
	hashhex := hex.EncodeToString(hash[:])
	hashname := FormatName(name, hashhex, fsys.hl)

	// Store in lookups.
	fsys.mu.Lock()
	fsys.m[name] = hashname
	fsys.r[hashname] = [2]string{name, hashhex}
	fsys.mu.Unlock()

	return hashname
}

// FormatName returns a hash name that inserts hash before the filename's
// extension. If no extension exists on filename then the hash is appended.
// Returns blank string the original filename if hash is blank. Returns a blank
// string if the filename is blank.
//
// TODO: don't export this.
func FormatName(filename, hash string, hl hashLocation) string {
	if filename == "" {
		return ""
	} else if hash == "" {
		return filename
	}

	dir, base := path.Split(filename)

	switch hl {
	case hashLocationFirstPeriod:
		if i := strings.Index(base, "."); i != -1 {
			return path.Join(dir, fmt.Sprintf("%s-%s%s", base[:i], hash, base[i:]))
		}
		return path.Join(dir, fmt.Sprintf("%s-%s", base, hash))

	case hashLocationStart:
		return path.Join(dir, fmt.Sprintf("%s.%s", hash, base))

	case hashLocationEnd:
		//Note, path.Ext() returns a value starting with a period (i.e.: .css),
		//hence the %s%s
		return path.Join(dir, fmt.Sprintf("%s.%s%s", base, hash, path.Ext(base)))

	default:
		//This should never occur since fsys.hashLocation is set by default and
		//can only be set to one of our defined values.
		return ""
	}
}

// ParseName splits formatted hash filename into its base & hash components.
func (fsys *FS) ParseName(filename string) (base, hash string) {
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	if hashed, ok := fsys.r[filename]; ok {
		return hashed[0], hashed[1]
	}

	return ParseName(filename)
}

// ParseName splits formatted hash filename into its base & hash components.
//
// TODO: don't export this.
func ParseName(filename string) (base, hash string) {
	if filename == "" {
		return "", ""
	}

	dir, base := path.Split(filename)

	// Extract pre-hash & extension.
	pre, ext := base, ""
	if i := strings.Index(base, "."); i != -1 {
		pre = base[:i]
		ext = base[i:]
	}

	// If prehash doesn't contain the hash, then exit.
	if !hashSuffixRegex.MatchString(pre) {
		return filename, ""
	}

	return path.Join(dir, pre[:len(pre)-65]+ext), pre[len(pre)-64:]
}

var hashSuffixRegex = regexp.MustCompile(`-[0-9a-f]{64}`)

// FileServer returns an http.Handler for serving FS files. It provides a
// simplified implementation of http.FileServer which is used to aggressively
// cache files on the client since the file hash is in the filename.
//
// Because FileServer is focused on small known path files, several features
// of http.FileServer have been removed including canonicalizing directories,
// defaulting index.html pages, precondition checks, & content range headers.
func FileServer(fsys fs.FS) http.Handler {
	hfsys, ok := fsys.(*FS)
	if !ok {
		hfsys = NewFS(fsys)
	}
	return &fsHandler{fsys: hfsys}
}

type fsHandler struct {
	fsys *FS
}

// ServeHTTP is used to serve a hashed static file.
func (h *fsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean up filename based on URL path.
	filename := r.URL.Path
	if filename == "/" {
		filename = "."
	} else {
		filename = strings.TrimPrefix(filename, "/")
	}
	filename = path.Clean(filename)

	// Read file from attached file system.
	f, hash, err := h.fsys.open(filename)
	if os.IsNotExist(err) {
		http.Error(w, "404 page not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	// Fetch file info. Disallow directories from being displayed.
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
		return
	} else if fi.IsDir() {
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return
	}

	// Cache the file aggressively if the file contains a hash.
	if hash != "" {
		w.Header().Set("Cache-Control", `public, max-age=31536000`)
		w.Header().Set("ETag", "\""+hash+"\"")
	}

	// Flush header and write content.
	switch f := f.(type) {
	case io.ReadSeeker:
		http.ServeContent(w, r, filename, fi.ModTime(), f.(io.ReadSeeker))
	default:
		// Set content length.
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))

		// Flush header and write content.
		w.WriteHeader(http.StatusOK)
		if r.Method != "HEAD" {
			io.Copy(w, f)
		}
	}
}

// HashLocationStart sets the hash to be prepended to the beginning of the filename.
// This is nice to keep the filename all together, but is a bit ugly for debugging
// in browser devtools since the on small/narrow screens the hash can take up most
// of the room.
//
// script.min.js becomes a1b2c3...d4e5f6.script.min.js.
func HashLocationStart() optionFunc {
	return func(f *FS) {
		f.hl = hashLocationStart
	}
}

// HashLocationEnd sets the hash to be appended to the end of the filename with the
// extension copied after the hash. This is nice to keep the filename all together;
// there really is no downside to this location.
//
// script.min.js becomes script.min.a1b2c3...d4e5f6.js.
func HashLocationEnd() optionFunc {
	return func(f *FS) {
		f.hl = hashLocationEnd
	}
}

// HashLocationFirstPeriod sets the hash to be added in the middle of the filename,
// specifically at the first period in the filename. This was the original designed
// hash location. There is really no benefit to this location, and it is a bit ugly
// since it breaks up the filename.
//
// script.min.js -> script-a1b2c3...d4e5f6.min.js
func HashLocationFirstPeriod() optionFunc {
	return func(f *FS) {
		f.hl = hashLocationFirstPeriod
	}
}
