package main

import (
	"go/types"
	"log"

	"golang.org/x/tools/go/loader"

	"honnef.co/go/tools/obj"
)

func main() {
	g, err := obj.OpenGraph("/tmp/graph")
	if err != nil {
		log.Fatal(err)
	}

	PKGS := []string{
		"archive/tar",
		"archive/zip",
		"bufio",
		"bytes",
		"compress/bzip2",
		"compress/flate",
		"compress/gzip",
		"compress/lzw",
		"compress/zlib",
		"container/heap",
		"container/list",
		"container/ring",
		"context",
		"crypto",
		"crypto/aes",
		"crypto/cipher",
		"crypto/des",
		"crypto/dsa",
		"crypto/ecdsa",
		"crypto/elliptic",
		"crypto/hmac",
		"crypto/internal/cipherhw",
		"crypto/md5",
		"crypto/rand",
		"crypto/rc4",
		"crypto/rsa",
		"crypto/sha1",
		"crypto/sha256",
		"crypto/sha512",
		"crypto/subtle",
		"crypto/tls",
		"crypto/x509",
		"crypto/x509/pkix",
		"database/sql",
		"database/sql/driver",
		"debug/dwarf",
		"debug/elf",
		"debug/gosym",
		"debug/macho",
		"debug/pe",
		"debug/plan9obj",
		"encoding",
		"encoding/ascii85",
		"encoding/asn1",
		"encoding/base32",
		"encoding/base64",
		"encoding/binary",
		"encoding/csv",
		"encoding/gob",
		"encoding/hex",
		"encoding/json",
		"encoding/pem",
		"encoding/xml",
		"errors",
		"expvar",
		"flag",
		"fmt",
		"go/ast",
		"go/build",
		"go/constant",
		"go/doc",
		"go/format",
		"go/importer",
		"go/internal/gccgoimporter",
		"go/internal/gcimporter",
		"go/parser",
		"go/printer",
		"go/scanner",
		"go/token",
		"go/types",
		"hash",
		"hash/adler32",
		"hash/crc32",
		"hash/crc64",
		"hash/fnv",
		"html",
		"html/template",
		"image",
		"image/color",
		"image/color/palette",
		"image/draw",
		"image/gif",
		"image/internal/imageutil",
		"image/jpeg",
		"image/png",
		"index/suffixarray",
		"internal/nettrace",
		"internal/pprof/profile",
		"internal/race",
		"internal/singleflight",
		"internal/syscall/unix",
		"internal/syscall/windows",
		"internal/syscall/windows/registry",
		"internal/syscall/windows/sysdll",
		"internal/testenv",
		"internal/trace",
		"io",
		"io/ioutil",
		"log",
		"log/syslog",
		"math",
		"math/big",
		"math/cmplx",
		"math/rand",
		"mime",
		"mime/multipart",
		"mime/quotedprintable",
		"net",
		"net/http",
		"net/http/cgi",
		"net/http/cookiejar",
		"net/http/fcgi",
		"net/http/httptest",
		"net/http/httptrace",
		"net/http/httputil",
		"net/http/internal",
		"net/http/pprof",
		"net/internal/socktest",
		"net/mail",
		"net/rpc",
		"net/rpc/jsonrpc",
		"net/smtp",
		"net/textproto",
		"net/url",
		"os",
		"os/exec",
		"os/signal",
		"os/user",
		"path",
		"path/filepath",
		"plugin",
		"reflect",
		"regexp",
		"regexp/syntax",
		"runtime",
		"runtime/debug",
		"runtime/internal/atomic",
		"runtime/internal/sys",
		"runtime/pprof",
		"runtime/pprof/internal/protopprof",
		"runtime/race",
		"runtime/trace",
		"sort",
		"strconv",
		"strings",
		"sync",
		"sync/atomic",
		"syscall",
		"testing",
		"testing/internal/testdeps",
		"testing/iotest",
		"testing/quick",
		"text/scanner",
		"text/tabwriter",
		"text/template",
		"text/template/parse",
		"time",
		"unicode",
		"unicode/utf16",
		"unicode/utf8",
		"unsafe",
		"vendor/golang_org/x/crypto/chacha20poly1305",
		"vendor/golang_org/x/crypto/chacha20poly1305/internal/chacha20",
		"vendor/golang_org/x/crypto/curve25519",
		"vendor/golang_org/x/crypto/poly1305",
		"vendor/golang_org/x/net/http2/hpack",
		"vendor/golang_org/x/net/idna",
		"vendor/golang_org/x/net/lex/httplex",
		"vendor/golang_org/x/text/transform",
		"vendor/golang_org/x/text/unicode/norm",
		"vendor/golang_org/x/text/width",
	}

	conf := &loader.Config{}
	for _, PKG := range PKGS {
		conf.Import(PKG)
		lprog, _ := conf.Load()
		pkg := lprog.Package(PKG).Pkg

		do(g, pkg)
	}

	if err := g.Close(); err != nil {
		log.Fatal(err)
	}
}

var seen = map[*types.Package]bool{}

func do(g *obj.Graph, pkg *types.Package) {
	if seen[pkg] {
		return
	}
	log.Println(pkg)
	seen[pkg] = true
	for _, imp := range pkg.Imports() {
		do(g, imp)
	}
	g.InsertPackage(pkg)
}
