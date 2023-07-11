//go:build ignore

package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"strings"
)

type Args struct {
	Source       string
	SourceMunged string
	Data         map[string][]map[string]string
}

func main() {
	fmt.Printf("Running %s go on %s\n", os.Args[0], os.Getenv("GOFILE"))

	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	fmt.Printf("  cwd = %s\n", cwd)
	fmt.Printf("  os.Args = %#v\n", os.Args)

	for _, ev := range []string{"GOARCH", "GOOS", "GOFILE", "GOLINE", "GOPACKAGE", "DOLLAR"} {
		fmt.Println("  ", ev, "=", os.Getenv(ev))
	}

	if len(os.Args) < 2 {
		panic("Missing argument, aborting")
	}

	args := Args{
		Source:       os.Args[1],
		SourceMunged: strings.ReplaceAll(os.Args[1], "-", ""),
		Data:         dataFromFiles(fmt.Sprintf("../../sources/%v/docs-data", os.Args[1])),
	}

	funcMap := template.FuncMap{
		"Title": strings.Title,
	}

	template := template.New("simple").Funcs(funcMap)
	template, err = template.Parse(`// Code generated by "extractmaps {{.Source}}"; DO NOT EDIT

package datamaps

import "github.com/overmindtech/sdp-go"

var {{.SourceMunged | Title }}Data = map[string][]TfMapData{
{{- range $key, $mappings := .Data}}
	"{{$key}}": { {{- range $mappings}}
		{
			Type:       "{{index . "type"}}",
			Method:     sdp.QueryMethod_{{index . "method"}},
			QueryField: "{{index . "query-field"}}",
			Scope:      "{{index . "scope"}}",
		},{{end}}
	},{{end}}
}
`)
	if err != nil {
		panic(err)
	}

	f, err := os.Create(fmt.Sprintf("%v.go", strings.ToLower(args.SourceMunged)))
	if err != nil {
		panic(err)
	}
	defer f.Close()

	fmt.Printf("Generating handler for %v\n", args.Source)
	err = template.Execute(f, args)
	if err != nil {
		panic(err)
	}
}

func dataFromFiles(path string) map[string][]map[string]string {
	result := map[string][]map[string]string{}
	entries, err := os.ReadDir(path)
	if err != nil {
		panic(err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		fmt.Println(e.Name())
		contents, err := os.ReadFile(fmt.Sprintf("%v/%v", path, e.Name()))
		if err != nil {
			panic(err)
		}

		var parsed map[string]any
		err = json.Unmarshal(contents, &parsed)
		if err != nil {
			panic(err)
		}

		queries, ok := parsed["terraformQuery"]
		if !ok {
			// skip if we don't have terraform query data
			continue
		}
		for _, qAny := range queries.([]interface{}) {
			q := qAny.(string)
			data := map[string]string{
				"type": parsed["type"].(string),
			}

			qSplit := strings.SplitN(q, ".", 2)
			data["query-type"] = qSplit[0]
			data["query-field"] = qSplit[1]

			data["scope"] = parsed["terraformScope"].(string)
			switch parsed["terraformMethod"].(string) {
			case "GET":
				data["method"] = "GET"
			case "SEARCH":
				data["method"] = "LIST"
			}

			result[data["query-type"]] = append(result[data["query-type"]], data)
		}
	}
	return result
}
