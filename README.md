hashfs
======

Implementation of io/fs.FS that inserts hashes into filenames to allow for aggressive HTTP caching and cache-busting.

For example, given a file path of `scripts/main.js`, the `hashfs.FS` filesystem will provide the server with a hashname of `scripts/main.js-a1b2c3...d4e5f6.js` (the hash is truncated for brevity in the example). When this file path is requested by the client, the server can verify the hash and return the contents with an aggressive `Cache-Control` header.


## Notes

This is a drop-in replacement for [`github.com/benbjohnson/hashfs`](github.com/benbjohnson/hashfs). You should not have to modify your code, unless you want to use some of the new [configurable options](#configurable-options).

See [https://pkg.go.dev/github.com/c9845/hashfs](https://pkg.go.dev/github.com/c9845/hashfs) for the API docs.


## Usage

*See the example directory for a full webserver code example.*

To use `hashfs`, first wrap your `fs.FS` in a `hashfs.FS` filesystem (`embed.FS` used as an example, but `os.DirFS` will work too):

``` go
//go:embed scripts stylesheets images
var embedFS embed.FS

var hfs = hashfs.NewFS(embedFS)
```

Then attach a `hashfs.FileServer()` to your router:

``` go
http.Handle("/static/", http.StripPrefix("/static/", hashfs.FileServer(hfs)))
```

Lastly, update your HTML templates to use the filename returned by `hfs.GetHashPath()` wherever you note the path to a static file.

``` go
func renderHTML(w io.Writer) {
	fmt.Fprintf(w, `<html>`)
	fmt.Fprintf(w, `<script src="/assets/%s"></script>`, hfs.GetHashPath("scripts/main.js"))
	fmt.Fprintf(w, `</html>`)
}
```


## Easier Usage

Use a custom template func. This is especially helpful if you store HTML outside of our golang code (which in most cases is true). Define a func to be added to your HTML template's `FuncMap` to handle translating the on-disk, defined name of a file and replace it with the hashed filename:

``` go 
func static(originalPath string) string {
	trimmedPath := strings.TrimPrefix(originalPath, "/static/")

	hashPath := hfs.GetHashPath(trimmedPath)

	return path.Join("/", "static", hashPath)
}

var myFuncMap = template.FuncMap{
	//func name used in templates, like {{static}}: defined func name.
	"static": static
}

myTemplates, err := template.New("name").Funcs(myFuncMap).ParseFS(myTemplatesFiles, pattern)
```

Then, inside your HTML templates:

```HTML
<html>
	<head>
		<link rel="stylesheet" href='{{static "/static/css/styles.min.css"}}'>
	</head>
	<body>
		<script src='{{static "/static/js/script.min.js"}}'></script>
	</body>
</html>
```


## Improvements over `github.com/benbjohnson/hashfs`:

- Configurable hash location in filename. 
	- Previously hash was inserted into filename at the first period which was a bit ugly, especially for filenames such as `script.min.js`. 
	- The new default location for the hash is at the end of the filename, with the file's extension copied after the hash.
	- Start of filename (`a1b2c3...d4e5f6.script.min.js`).
	- End of filename [default] (`script.min.js-a1b2c3...d4e5f6.js`).
	- First period [legacy] (`script-a1b2c3...d4e5f6.min.js`).
- Additional configuration options: 
	- Hash algorithm (anything from fulfills `crypto.Hash`).
	- Cache-Control max age.
	- Hash length.
- Improved documentation within code.
- Example implementation.
- Example, documentation, and details around `FuncMap` func to handle translating original filename to hash filename.


## Configurable Options:

You can provide one, or any combination, of the below configuration funcs to configure `hashfs` when you call `NewFS()`.

- `HashLocationStart()`, `HashLocationEnd()`, or `HashLocationFirstPeriod()`.
- `HashAlgo()`.
- `MaxAge()`.
- `HashLength()`.

```go
hfs := hashfs.NewFS(fsys, hashfs.HashLocationFirstPeriod(), hashfs.HashAlgo(crypto.MD5), hashfs.MaxAge(time.Duration(1 * time.Hour), hashfs.HashLength(10))
```
