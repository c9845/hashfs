/*
Package hashfs handles cache-busting of files by adding a hash of each file's
contents to the filename.

# How This Works:
  - You provide your files, as an [fs.FS].
  - When your binary runs, a hash is calculated of a static file's contents.
  - The hash is appended to the file's name.
  - The new filename is rewritten into your HTML code.
  - When a browser requests a static file, using the filename-with-hash, the underlying
    file is looked up and served with aggressive caching headers.

# Usage:
  - Call [hashfs.NewFS] before parsing your HTML templates.
  - Define a func to call [HFS.GetHashPath], and add it to your [html/template.FuncMap].
  - Modify your HTML templates to use the func defined in your [html/template.FuncMap]
    for each static file you want to cache-bust.
  - Call [hashfs.FileServer] in your HTTP router on the endpoint you serve static
    files from.

# Example:
See the example/example.go file in the source repo.

# Example FuncMap func:

	  func static(originalPath string) (hashPath string) {
		//Handle dev mode, serve non-hashed files.
		if devMode {
			return originalPath
		}

		//Trim path, if needed.
		//
		//For example, if your static files are served off of www.example.co/static/
		//and your fs.FS lists files "inside" the /static/ directory from your source
		//code repo, you need to remove the /static/ part of the URL to find the
		//matching source file. The fs.FS files will not have /static/ in their paths
		//since, the fs.FS just contains the files "inside" the /static/ directory.
		trimmedPath := strings.TrimPrefix(originalPath, "/static/")

		//Get the hashPath. This is where the hash is calculated, if it has not been
		//already (this static func was already called on this originalPath when
		//another template was being built in this run of your binary).
		hashPath := yourHashFS.GetHashPath(trimmedPath)

		//Now, we need to add the /static/ back to the path since that is how the
		//browser expects it.
		return path.Join("/", "static", hashPath)
	  }

# Definitions:
  - original path: the path to the on-disk source file.
  - hash path: the path where the filename includes the hash of the file's contents.
  - original name: the filename of the on-disk source file.
  - hash name: the filename inclusive of the hash of the file's contents.
*/
package hashfs

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Ensure file system implements interface.
var _ fs.FS = (*HFS)(nil)

// HFS represents an fs.FS with additional lookup tables for storing the calculated
// hashes of each file's contents. The hashes are used for aggressive client-side
// caching and cache-busting.
type HFS struct {
	fsys fs.FS

	//Lookup tables.
	//Note that the lookup tables store a path to each file, not just a filename.
	mu                     sync.RWMutex
	originalPathToHashPath map[string]string  //a cache so we don't have to recalculate hash over and over.
	hashPathReverse        map[string]reverse //get original path and hash from hash path.

	//Options.
	//TODO: add options for hash function (MD5 like S3?); Cache-Control header mx-age,...
	hashLocation hashLocation
}

// reverse stores the original name and the calculated hash for a file for use in
// the reverse lookup table. This information is used to serve the on-disk source
// file from the hash path when the file is requested.
//
// I.e.: when the browser requests a file via the hash path, we need a way to look
// up the actual file's contents to serve. This is used to get the originalPath from
// the hashPath which is then used to look up the file in the fs.FS.
//
// The hash is used to set the Etag header. This way we don't have to "rip out" the
// hash from the hashPath.
type reverse struct {
	originalPath string
	hash         string
}

// hashLocation defines the position of the hash in the filename.
type hashLocation int

const (
	hashLocationStart       hashLocation = iota //script.min.js -> a1b2c3...d4e5f6.script.min.js
	hashLocationFirstPeriod                     //script.min.js -> script-a1b2c3...d4e5f6.min.js; original designed hash location
	hashLocationEnd                             //script.min.js -> script.min.a1b2c3...d4e5f6.js

	//default is "end" since this looks the best in browser dev tools.
	//"first period" was the legacy location.
	hashLocationDefault = hashLocationEnd
)

// optionFunc used to modify the way the an HFS works.
type optionFunc func(*HFS)

//
// There are a few ways to provide the hash location to the NewFS() func. Using a
// func-per-location seems like the cleanest.
//
// We could have exported the hashLocationStart/FirstPeriod/End consts and used
// them directly in NewFS(), but we would have needed a "SetHashLocation()" option
// func anyway (because option funcs are nice for future expansion over just an
// argument to NewFS()) and then you end up with ugly NewFS() calls:
// hashfs.New(f, hashfs.SetHashLocation(hashfs.HashLocationStart)).
//
// We could have not used optional funcs, and instead added an argument to the NewFS()
// func, but this would break existing usage. It would also be annoying to have to
// add a new argument for future options.
//

// HashLocationStart sets the hash to be prepended to the beginning of the filename.
// script.min.js becomes a1b2c3...d4e5f6-script.min.js.
//
// This is nice to keep the filename all together, but is a bit ugly for debugging
// in browser devtools since the on small/narrow screens the hash can take up all
// of the room where a filename will be displayed making identifying a specific file
// difficult.
func HashLocationStart() optionFunc {
	return func(f *HFS) {
		f.hashLocation = hashLocationStart
	}
}

// HashLocationEnd sets the hash to be appended to the end of the filename with the
// extension copied after the hash. script.min.js becomes script.min.js-a1b2c3...d4e5f6.js.
// This is the default hash location.
//
// This is nice to keep the filename all together; there really is no downside to this
// location.
func HashLocationEnd() optionFunc {
	return func(f *HFS) {
		f.hashLocation = hashLocationEnd
	}
}

// HashLocationFirstPeriod sets the hash to be added in the middle of the filename,
// specifically at the first period in the filename. This was the original designed
// hash location. script.min.js becomes script-a1b2c3...d4e5f6.min.js
//
// There is really no benefit to this location, and it is a bit ugly since it breaks
// up the filename.
func HashLocationFirstPeriod() optionFunc {
	return func(f *HFS) {
		f.hashLocation = hashLocationFirstPeriod
	}
}

// NewFS returns the provided fs.FS with additional tooling to support calculating the
// hash of each file's contents for caching purposes.
//
// optionFuncs are used for modifying the HFS. Optional funcs were used, versus just
// additional arguments, since this allows for future expansion without breaking
// existing uses and is cleaner than empty unused arguments.
func NewFS(fsys fs.FS, options ...optionFunc) *HFS {
	f := &HFS{
		fsys:                   fsys,
		originalPathToHashPath: make(map[string]string),
		hashPathReverse:        make(map[string]reverse),
		hashLocation:           hashLocationDefault,
	}

	//Apply any options.
	for _, option := range options {
		option(f)
	}

	return f
}

// Open returns a reference to the file at the provided path. The path could be an
// original path or a hash path. If a hash path is given, the original path will be
// looked up to return the file with.
//
// This func is necessary for HFS to implement fs.FS. You should not need need to
// call this func directly.
func (hfs *HFS) Open(path string) (f fs.File, err error) {
	f, _, err = hfs.open(path)
	return
}

// open returns a reference to the file at the provided path. The path could be an
// original path or a hash path. If a hash path is given, the original path will be
// looked up to return the file with.
//
// This differs from Open because the hash of the file at the provided path is also
// returned. The hash is used to set the Etag header.
func (hfs *HFS) open(path string) (f fs.File, hash string, err error) {
	//Try looking up the path in our table of hash paths. If the path is found, this
	//means the given path is a hash path. The returned original path can be used to
	//look up the underlying source file.
	//
	//If the path is not found, than most likely the path is an original path. Just
	//use it as-is to look up the source file.
	hfs.mu.RLock()
	reverse, exists := hfs.hashPathReverse[path]
	if exists {
		hash = reverse.hash
		path = reverse.originalPath
	}
	hfs.mu.RUnlock()

	f, err = hfs.fsys.Open(path)
	return
}

// GetHashPath returns the hashPath for a provided originalPath. This will calculate
// the hash for the file located at the originalPath if the hash has not already been
// calculated.
//
// The hash is calculated once and stored in the lookup tables for reuse. This removes
// the need to recalculate the hash each time GetHashPath is called for the same
// originalPath.
func (hfs *HFS) GetHashPath(originalPath string) (hashPath string) {
	//Check if hashPath has already been created and is cached.
	hfs.mu.RLock()
	hp, exists := hfs.originalPathToHashPath[originalPath]
	if exists {
		hfs.mu.RUnlock()
		return hp
	}
	hfs.mu.RUnlock()

	//Hash has not already been calculated, look up file and calculate hash.
	//
	//On error, just return the original filename this way the file can still
	//be served.
	//TODO: somehow notify of this error? log = ugly. panic = ugly. return err?
	fileContents, err := fs.ReadFile(hfs.fsys, originalPath)
	if err != nil {
		return originalPath
	}

	//Calculate the hash.
	hash := hfs.calculateHash(fileContents)

	//Add the hash the filename.
	//Format the filename with the hash.
	dir, filename := path.Split(originalPath)
	fileNameWithHash := hfs.addHashToFilname(filename, hash)

	//Build the path to the file with the hash filename.
	hashPath = path.Join(dir, fileNameWithHash)

	//Store mappings for reuse in the future.
	hfs.mu.Lock()
	hfs.originalPathToHashPath[originalPath] = hashPath
	hfs.hashPathReverse[hashPath] = reverse{originalPath, hash}
	hfs.mu.Unlock()

	return
}

// calculateHash calculates the hash of a file's contents and returns it with hex
// encoding.
//
// This functionality was separated out of GetHashPath in case we add support for
// alternative hash algorithms in the future (i.e.: MD5 like S3 uses for Etag header).
func (hfs *HFS) calculateHash(fileContents []byte) (hash string) {
	h := sha256.Sum256(fileContents)
	hash = hex.EncodeToString(h[:])
	return
}

// addHashToFilename adds the hash to the originalName at the location specified by
// hashLocation. If originalName or hash is blank, the returned hashName will also
// be blank.
func (hfs *HFS) addHashToFilname(originalName, hash string) (hashName string) {
	//Quick validation. Neither of these should ever be blank.
	if originalName == "" {
		return
	}
	if hash == "" {
		return
	}

	//Add the hash to the filename.
	switch hfs.hashLocation {
	case hashLocationFirstPeriod:
		//Handle if the filename doesn't have a period in it. This shouldn't really
		//ever occur since a filename should have an extension. In this case, just
		//append the hash to the filename, separating it with a dash to make it
		//stand out a bit.
		i := strings.Index(originalName, ".")
		if i == -1 {
			hashName = originalName + "-" + hash
			return
		}

		//Add the hash just before the first period, separating it with a dash to
		//make it stand out a bit.
		hashName = originalName[:i] + "-" + hash + originalName[i:]
		return

	case hashLocationStart:
		//Add the hash to the beginning of the filename, separating it with a dash
		//to make it stand out a bit.
		hashName = hash + "-" + originalName
		return

	case hashLocationEnd:
		//Add the hash to the end of the filename, duplicating the file's extension
		//after the hash to prevent breaking MIME type determination in browsers.
		//The filename and hash are separated with a dash to make it stand out a bit.
		//
		//Note, path.Ext() returns a value starting with a period (i.e.: .css).
		hashName = originalName + "-" + hash + path.Ext(originalName)
		return

	default:
		//This should never occur since fsys.hashLocation is set by default and
		//can only be set to one of our defined funcs. This is just here since all
		//switches should have a default.
		return
	}
}

//
//###################################################################################
//

// hfsHandler is used to define a ServeHTTP func that uses our customized fs.FS.
type hfsHandler struct {
	hfs *HFS
}

// FileServer returns an http.Handler for serving files from our custom FS. It
// provides a simplified implementation of http.FileServer which is used to
// aggressivley cache files on the client. You would use this in the same manner as
// http.FileServer. Ex.: http.FileServer(http.FS(someStaticFS)) -> hashfs.FileServer(hfs).
//
// Because FileServer is focused on small known path files, several features
// of http.FileServer have been removed including canonicalizing directories,
// defaulting index.html pages, precondition checks, & content range headers.
func FileServer(fsys fs.FS) http.Handler {
	//Check if the fsys is actually our custom HFS that encapsulates an fs.FS.
	hfs, ok := fsys.(*HFS)
	if !ok {
		panic("unknown FS")
	}

	return &hfsHandler{hfs}
}

// ServeHTTP serves files from our custom FS.
//
// This func is necessary to fulfill the requirements of hfsHandler to be used as
// an http.Handler. It would be extremely odd to need to call this func directly.
func (hh *hfsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	//Get path of file being requested. This should match a hash path, but could be
	//an original path if a hash was never calculated for the file.
	filePath := r.URL.Path

	// Clean up filePath based on URL path.
	if filePath == "/" {
		filePath = "."
	} else {
		filePath = strings.TrimPrefix(filePath, "/")
	}
	filePath = path.Clean(filePath)

	// Get the file from our fs.FS.
	//
	//This will look up the original file if the filePath is a hash path. If the
	//filePath is an original path (i.e. we don't have this original path in our
	//lookup tables), then the given path is used to look up the file with.
	f, hash, err := hh.hfs.open(filePath)
	if os.IsNotExist(err) {
		//Handle if no file exists at the given path.
		httpErrorCode := http.StatusNotFound
		http.Error(w, http.StatusText(httpErrorCode), httpErrorCode)
		return
	} else if err != nil {
		//Handle if some other error occured.
		httpErrorCode := http.StatusInternalServerError
		http.Error(w, http.StatusText(httpErrorCode), httpErrorCode)
		return
	}
	defer f.Close()

	//Get file's info.
	//
	//This is used to make sure a directory wasn't mistakenly requested or some
	//other strange error occured with the file.
	info, err := f.Stat()
	if err != nil {
		httpErrorCode := http.StatusInternalServerError
		http.Error(w, http.StatusText(httpErrorCode), httpErrorCode)
		return
	} else if info.IsDir() {
		httpErrorCode := http.StatusForbidden
		http.Error(w, http.StatusText(httpErrorCode), httpErrorCode)
		return
	}

	//Set aggressive caching headers.
	//
	//We check if a hash exists to prevent setting caching headers on non-hashed
	//files. We don't want to cache these files aggressively since if the source
	//changes, the browser won't know this and thus continue serving the old files.
	//
	//Note that if you use Cloudflare free tier, Cloudflare will apply a "W/" to
	//the beginning of the Etag value automatically. The "W" represents a weak Etag
	//value. For some reason Cloudflare thinks they know better here about strong
	//versus weak Etag values.
	//https://developers.cloudflare.com/cache/reference/etag-headers/#strong-etags
	if hash != "" {
		w.Header().Set("Cache-Control", hh.hfs.getCacheControl())
		w.Header().Set("ETag", hash)

		//We don't set a Last-Modified header since the file info available for
		//files in an fs.FS does not include when the file was modified. Instead,
		//the ModTime() is when the binary was build and the files were embedded.
	}

	//Write out the file's contents.
	switch f := f.(type) {
	case io.ReadSeeker:
		http.ServeContent(w, r, filePath, info.ModTime(), f)
	default:
		// Set content length.
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))

		// Flush header and write content.
		w.WriteHeader(http.StatusOK)
		if r.Method != "HEAD" {
			io.Copy(w, f)
		}
	}
}

// getCacheControl creates the value stored in the Cache-Control header. This was
// separated out into a function for better testing and future ability to customize
// the max-age via an optionFunc.
func (hfs *HFS) getCacheControl() string {
	maxAge := strconv.Itoa(365 * 24 * 60 * 60)

	return `public, max-age="` + maxAge + "`, immutable"
}

//printEmbeddedFileList used as development tool only.
// func (hfs *HFS) printEmbeddedFileList() (output []string) {
// 	//the directory "." means the root directory of the embedded file.
// 	const startingDirectory = "."

// 	err := fs.WalkDir(hfs.fsys, startingDirectory, func(path string, d fs.DirEntry, err error) error {
// 		output = append(output, path)
// 		return nil
// 	})
// 	if err != nil {
// 		output = []string{"error walking embedded directory", err.Error()}
// 		return
// 	}

// 	return
// }
