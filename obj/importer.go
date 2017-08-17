package obj

import (
	"crypto/sha1"
	"encoding/hex"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"honnef.co/go/tools/obj/cgo"

	"golang.org/x/tools/go/buildutil"
)

const version = "1"

type Package struct {
	*types.Package
	*types.Info

	Files []*ast.File
}

// Importer manages the importing of Go packages from source form and
// from our internal export format.
type Importer struct {
	Fset *token.FileSet

	g         *Graph
	build     build.Context
	augmented map[*types.Package]*Package
	pkgs      map[string]*types.Package
	checker   *types.Config
}

func NewImporter(g *Graph, build build.Context) *Importer {
	imp := new(Importer)
	*imp = Importer{
		Fset: token.NewFileSet(),

		g:         g,
		build:     build,
		pkgs:      map[string]*types.Package{},
		augmented: map[*types.Package]*Package{},
		checker: &types.Config{
			Importer: imp,
		},
	}
	return imp
}

// Package returns an augmented representation of a package. Package
// can be used on the result of Import to get access to additional
// data, such as the AST and per-node type information.
func (imp *Importer) Package(pkg *types.Package) *Package {
	panic("not implemented")
}

// Import implements the types.Importer interface.
func (imp *Importer) Import(path string) (*types.Package, error) {
	panic("not implemented")
}

// ImportFrom implements the types.ImporterFrom interface.
func (imp *Importer) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}

	bpkg, err := imp.build.Import(path, srcDir, 0)
	if err != nil {
		return nil, err
	}

	if pkg, ok := imp.pkgs[bpkg.ImportPath]; ok && pkg.Complete() {
		return pkg, nil
	}

	if imp.g.HasPackage(bpkg.ImportPath) {
		log.Println("using graph for", bpkg.ImportPath)
		// XXX validate checksum
		// XXX load AST from somewhere
		// XXX load packageinfo from somewhere
		return imp.g.Package(bpkg.ImportPath), nil
	}
	log.Println("found no archive for", bpkg.ImportPath)

	var files []*ast.File
	for _, f := range bpkg.GoFiles {
		af, err := buildutil.ParseFile(imp.Fset, &imp.build, nil, bpkg.Dir, f, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, af)
	}

	if len(bpkg.CgoFiles) > 0 {
		log.Println("processing cgo files")
		cgoFiles, err := cgo.ProcessCgoFiles(bpkg, imp.Fset, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, cgoFiles...)
	}

	info := &types.Info{}
	pkg, err := imp.checker.Check(path, imp.Fset, files, info)
	if err != nil {
		return nil, err
	}
	imp.augmented[pkg] = &Package{
		Package: pkg,
		Info:    info,
		Files:   files,
	}
	imp.g.InsertPackage(pkg)
	imp.pkgs[bpkg.ImportPath] = pkg
	return pkg, nil
}

// Delete deletes pkg, and all of its reverse dependencies, from the
// cache of imported packages.
func (imp *Importer) Delete(pkg *types.Package) {
	panic("not implemented")
}

type lookupKey struct {
	goarch     string
	goos       string
	cgo        bool
	tags       []string
	importPath string
}

func (key lookupKey) path() string {
	cgo := "nocgo"
	if key.cgo {
		cgo = "cgo"
	}
	tags := "tags-" + strings.Join(key.tags, "-")
	return filepath.Join(key.goarch, key.goos, cgo, tags, key.importPath) + ".tar"
}

func (imp *Importer) cacheLookupKey(bpkg *build.Package) lookupKey {
	// export data is namespaced by GOARCH+GOARM, GOOS, CGO_ENABLED
	// and build tags.
	return lookupKey{
		goarch:     imp.build.GOARCH,
		goos:       imp.build.GOOS,
		cgo:        imp.build.CgoEnabled,
		tags:       imp.build.BuildTags,
		importPath: bpkg.ImportPath,
	}
}

func (imp *Importer) cacheExpiryKey(bpkg *build.Package) (string, error) {
	lists := [][]string{
		bpkg.GoFiles,
		bpkg.CgoFiles,
		bpkg.TestGoFiles,
		bpkg.XTestGoFiles,
		bpkg.CFiles,
		bpkg.CXXFiles,
		bpkg.MFiles,
		bpkg.HFiles,
		bpkg.FFiles,
		bpkg.SFiles,
		bpkg.SwigFiles,
		bpkg.SwigCXXFiles,
		bpkg.SysoFiles,
	}

	var b []byte
	var mtime time.Time
	for _, list := range lists {
		for _, f := range list {
			b = append(b, f...)
			b = append(b, 0)
			fi, err := os.Stat(filepath.Join(bpkg.Dir, f))
			if err != nil {
				return "", err
			}
			if mod := fi.ModTime(); mod.Before(mtime) {
				mtime = mod
			}
		}
	}
	s := strconv.FormatInt(mtime.UnixNano(), 10)
	b = append(b, s...)
	b = append(b, 0)
	b = append(b, runtime.Version()...)
	b = append(b, 0)
	b = append(b, version...)
	b = append(b, 0)

	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:]), nil
}
