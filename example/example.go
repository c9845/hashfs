/*
Package example provides an example HTTP server for showing how hashfs is implemented
and the results you can inspect.

Simply run with go run example.com and open a browser to localhost:8080.

Implementation of HashFS:
  - Your source files are read via embed.FS.
  - Your static files are provided to hashfs.NewFS.
  - You define a func (see static below) to rewrite your URLs from the original name
    to the name generated by hashfs based on each file's contents.
  - You modify your your HTML templates to use the defined func for each static file.
    see {{static ...}} in website/templates/index.html.
  - When your HTML templates are parsed, the defined func rewrites the URLs to each
    static file to include a hash of each file's contents.
*/
package main

import (
	"embed"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"strings"
	"text/template"

	"github.com/c9845/hashfs"
)

//go:embed website
var embeddedFiles embed.FS

// This is where we will store our customized fs for static files.
var staticFilesHashFS *hashfs.HFS

// Store our parsed HTML templates for use in HTTP responses.
var templates *template.Template

func init() {
	//Initialize hashfs that will hash each file's contents.
	//
	//Since the "static" directory is a subdirectory of the directory we embedded,
	//navigate into the subdirectory.
	staticDir, err := fs.Sub(embeddedFiles, "website/static")
	if err != nil {
		panic(err)
	}

	staticFilesHashFS = hashfs.NewFS(staticDir)

	//Add our custom func that handles rewriting URLs to the funcs that are usable
	//in HTML templates. This will make a {{static}} callable just like {{if}}
	//or {{range}}.
	funcMap := template.FuncMap{
		"static": static,
	}

	//Parse our HTML templates.
	templatesFS, err := fs.Sub(embeddedFiles, "website/templates")
	if err != nil {
		panic(err)
	}
	t, err := template.New("").Funcs(funcMap).ParseFS(templatesFS, "*.html")
	if err != nil {
		panic(err)
	}
	templates = t

	//Test our static func, just to make sure it is working as we expect.
	originalPath := "/static/css/styles.css"
	hashPath := static(originalPath)
	if hashPath == originalPath || hashPath == "" {
		log.Fatalln("Could not get hash path", hashPath, originalPath)
		return
	}
}

func main() {
	//Run the HTTP server.
	http.HandleFunc("/", handler)
	http.Handle("/static/", http.StripPrefix("/static/", hashfs.FileServer(staticFilesHashFS)))

	port := "8080"
	host := "127.0.0.1"
	hostPort := net.JoinHostPort(host, port)
	log.Println("Listening on port:", port)
	log.Fatal(http.ListenAndServe(hostPort, nil))
}

// handler responds to an HTTP request on an endpoint.
func handler(w http.ResponseWriter, r *http.Request) {
	err := templates.ExecuteTemplate(w, "index.html", nil)
	if err != nil {
		log.Println("handler error", err)
		return
	}
}

// static is used to translate filenames from a standard "styles.min.css" to the
// version that incorporates a hash generated by hashfs. This is done for cache-
// busting purposes.
//
// Use as {{static "/static/css/styles.css"}} in conjunction with
// http.Handle("/static/", http.StripPrefix("/static/", hashfs.FileServer(staticFilesFS)))
func static(originalPath string) string {
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
	hashPath := staticFilesHashFS.GetHashPath(trimmedPath)

	//Now, we need to add the /static/ back to the path since that is how the
	//browser expects it.
	return path.Join("/", "static", hashPath)
}
