/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"text/template"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"stash.ovh.net/cloud/integration/cmd/tools/buildergenv2/pkg"
	"stash.ovh.net/cloud/integration/cmd/tools/buildergenv2/pkg/model"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "buildergen",
	Short: "Generate builders for your structs",
	Long:  `Generate builders for your Golang structs`,
	Run:   GenerateBuilders,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

type config struct {
	Path     string
	Builders []builder
}

type builder struct {
	Filename string
	Structs  []string
	Pointer  bool
}

var C []config

var all bool

func init() {
	rootCmd.PersistentFlags().BoolVar(&all, "all", false, "Generate all builder of sub directories")
}

func readConfig(path string) error {
	bytes, err := os.ReadFile(path)
	cobra.CheckErr(err)

	var localConfig config
	err = json.Unmarshal(bytes, &localConfig)
	if err != nil {
		return err
	}

	dirConfig := config{
		Path:     filepath.Dir(path),
		Builders: make([]builder, 0, len(localConfig.Builders)),
	}
	for _, builder := range localConfig.Builders {
		builder.Filename = dirConfig.Path + "/" + builder.Filename
		dirConfig.Builders = append(dirConfig.Builders, builder)
	}
	C = append(C, dirConfig)
	return nil
}

func readAndMergeConfigs(baseDir string) error {
	// Walk through all subdirectories and read config files
	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err // Skip if there is an error accessing a file/directory
		}

		// If it's a file named "builders.json", try loading it with viper
		if !d.IsDir() && filepath.Base(path) == "builders.json" {
			if err := readConfig(path); err != nil {
				return err
			}
		}

		return nil
	})

	return err
}

type result struct {
	filePath string
	structs  pkg.Structs
}

func GenerateBuilders(cmd *cobra.Command, args []string) {
	// Read and merge configurations
	if all {
		err := readAndMergeConfigs(".")
		cobra.CheckErr(err)
	} else {
		err := readConfig("./builders.json")
		cobra.CheckErr(err)
	}

	var group errgroup.Group
	var mutex sync.Mutex
	var results []result

	for _, C := range C {
		group.Go(func() error {

			structsToGenerate := []*pkg.Struct{}

			for _, structSource := range C.Builders {
				structPkg, err := pkg.SourceMode(structSource.Filename)
				if err != nil {
					return err
				}

				for _, structName := range structSource.Structs {
					var s *pkg.Struct
					if s, err = generateForStruct(structPkg, structName, structSource.Pointer); err != nil {
						return err
					}
					structsToGenerate = append(structsToGenerate, s)
				}
			}

			aggregatedStructs := pkg.Structs{
				Imports: make(map[string]string),
				Structs: make([]*pkg.Struct, 0),
			}

			for _, structToGenerate := range structsToGenerate {
				if aggregatedStructs.Package == "" {
					aggregatedStructs.Package = structToGenerate.Package
				} else if aggregatedStructs.Package != structToGenerate.Package {
					return errors.New("found different packages for struct to generate")
				}

				for importName, importPath := range structToGenerate.Imports {
					aggregatedStructs.Imports[importName] = importPath
				}

				aggregatedStructs.Structs = append(aggregatedStructs.Structs, structToGenerate)
			}

			mutex.Lock()
			results = append(results, result{filePath: C.Path, structs: aggregatedStructs})
			mutex.Unlock()

			return nil
		})
	}

	err := group.Wait()
	if err != nil {
		log.Fatal(err)
	}

	structTemplate, err := template.New("struct").Funcs(template.FuncMap{
		"renamedImport": func(importName, importPath string) string {
			spl := strings.Split(importPath, "/")
			if spl[len(spl)-1] == importName {
				return ""
			}
			return importName
		},
		"add": func(a, b int) int {
			return a + b
		},
		"toUpper": func(name string) string {
			return cases.Title(language.English, cases.NoLower).String(name)
		},
		"toLower": func(name string) string {
			return strings.ToLower(name[0:1]) + name[1:]
		},
		"firstToLower": func(name string) string {
			return strings.ToLower(name[0:1])
		},
		"unescapeHtml": func(name string) string {
			return html.UnescapeString(name)
		},
		"prettifyFieldName": func(name string) string {
			return strings.TrimRight(name, "_")
		},
	}).Parse(pkg.Tmpl)
	if err != nil {
		log.Fatal(err)
	}

	for _, res := range results {
		file, err := os.Create(res.filePath + "/builders_generated.go")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		err = structTemplate.Execute(file, res.structs)
		if err != nil {
			log.Fatal(err)
		}
	}

	cmdGoimports := exec.Command("goimports", "-local", "stash.ovh.net", "-w", ".")
	cmdGoimports.Stdout = os.Stdout
	cmdGoimports.Stderr = os.Stderr
	err = cmdGoimports.Run()
	if err != nil {
		log.Fatal(err)
	}

}

func generateForStruct(structPackage *model.Package, structName string, noPointer bool) (*pkg.Struct, error) {
	for _, str := range structPackage.Structs {
		if str.Name == structName {
			// Get all required imports, and generate unique names for them all.
			im := structPackage.ImportsForStruct(structName)

			// Sort keys to make import alias generation predictable
			sortedPaths := make([]string, len(im))
			x := 0
			for pth := range im {
				sortedPaths[x] = pth
				x++
			}
			sort.Strings(sortedPaths)

			packagesName, err := pkg.CreatePackageMap(sortedPaths)
			if err != nil {
				return nil, err
			}

			s := &pkg.Struct{
				Package: structPackage.Name,
				Name:    str.Name,
				Imports: packagesName,
				Fields:  make([]pkg.Field, len(str.Fields)),
				Pointer: noPointer,
			}

			for i, field := range str.Fields {
				name := field.Name
				if name == "" {
					typeNameSplitted := strings.Split(field.Type.String(packagesName, structPackage.PkgPath), ".")
					name = typeNameSplitted[len(typeNameSplitted)-1]
				}

				if len(name) > 0 && unicode.IsUpper(rune(name[0])) {
					return nil, fmt.Errorf("field %s in struct %s is exported (%s)", name, structName, structPackage.PkgPath)
				}

				hasFromString := false
				if namedType, ok := field.Type.(*model.NamedType); ok {
					hasFromString = namedType.HasFromStringMethod
				}

				var optional bool
				value, ok := reflect.StructTag(strings.Trim(field.Tags, "`")).Lookup("domain")
				if ok && value == "optional" {
					optional = true
				}

				s.Fields[i] = pkg.Field{
					Name:       name,
					Type:       field.Type.String(packagesName, structPackage.PkgPath),
					Tags:       strings.ReplaceAll(field.Tags, "jsontag", "json"),
					Optional:   optional,
					FromString: hasFromString,
				}
			}

			return s, nil
		}
	}

	return nil, fmt.Errorf("struct %s not found", structName)
}
