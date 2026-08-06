// schema-validate: 校验 schema/sdk/v1 下所有 .json schema 文件的合法性，
// 然后用 fixtures/ 目录里的实例校验对应 schema。
//
// 用法：
//
//	go run ./tools/schema-validate
//	go run ./tools/schema-validate --root <path>
//
// fixture 约定：fixture 文件第一行字段 "$schema" 指向同 root 下相对路径或完整 $id。
// 没有 $schema 字段的 fixture 文件会被跳过（带警告）。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// idPrefixFromRoot 从 root 路径推导 schema $id 前缀:
// schema/sdk/v1 → https://biumind.dev/sdk/v1/
// schema/release/v1 → https://biumind.dev/release/v1/
// 规则:root 去掉顶层 "schema/" 后拼到 https://biumind.dev/<...>/。
func idPrefixFromRoot(root string) string {
	rel := filepath.ToSlash(filepath.Clean(root))
	rel = strings.TrimPrefix(rel, "schema/")
	rel = strings.TrimPrefix(rel, "./")
	return "https://biumind.dev/" + rel + "/"
}

func main() {
	root := flag.String("root", "schema/sdk/v1", "schema 根目录")
	flag.Parse()

	if err := run(*root); err != nil {
		fmt.Fprintf(os.Stderr, "schema-validate: %v\n", err)
		os.Exit(1)
	}
}

func run(root string) error {
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("root not dir: %s", root)
	}

	idPrefix := idPrefixFromRoot(root)

	schemaFiles, err := walkJSON(root, "fixtures", idPrefix)
	if err != nil {
		return err
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)

	for _, sf := range schemaFiles {
		raw, err := os.ReadFile(sf.abs)
		if err != nil {
			return fmt.Errorf("read %s: %w", sf.rel, err)
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			return fmt.Errorf("parse %s: %w", sf.rel, err)
		}
		if err := c.AddResource(sf.id, doc); err != nil {
			return fmt.Errorf("add %s: %w", sf.rel, err)
		}
	}

	compiled := 0
	for _, sf := range schemaFiles {
		if _, err := c.Compile(sf.id); err != nil {
			return fmt.Errorf("compile %s: %w", sf.rel, err)
		}
		compiled++
	}
	fmt.Printf("✓ compiled %d schemas under %s\n", compiled, root)

	fixturesDir := filepath.Join(root, "fixtures")
	fixtureFiles, err := walkJSON(fixturesDir, "", idPrefix)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("· no fixtures/ dir; skipping fixture validation")
			return nil
		}
		return err
	}
	if len(fixtureFiles) == 0 {
		fmt.Println("· fixtures/ empty; skipping fixture validation")
		return nil
	}

	checked, skipped := 0, 0
	for _, ff := range fixtureFiles {
		raw, err := os.ReadFile(ff.abs)
		if err != nil {
			return fmt.Errorf("read fixture %s: %w", ff.rel, err)
		}
		var head struct {
			Schema string `json:"$schema"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return fmt.Errorf("parse fixture %s: %w", ff.rel, err)
		}
		if head.Schema == "" {
			fmt.Fprintf(os.Stderr, "  · skip %s (missing $schema field)\n", ff.rel)
			skipped++
			continue
		}
		schemaID := head.Schema
		if !strings.HasPrefix(schemaID, "http") {
			schemaID = idPrefix + strings.TrimPrefix(schemaID, "./")
		}
		sch, err := c.Compile(schemaID)
		if err != nil {
			return fmt.Errorf("fixture %s → schema %s: %w", ff.rel, schemaID, err)
		}
		instance, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			return fmt.Errorf("parse fixture %s: %w", ff.rel, err)
		}
		if err := sch.Validate(instance); err != nil {
			return fmt.Errorf("fixture %s fails %s:\n%v", ff.rel, schemaID, err)
		}
		checked++
	}
	fmt.Printf("✓ validated %d fixtures (%d skipped)\n", checked, skipped)
	return nil
}

type schemaFile struct {
	abs string
	rel string
	id  string
}

func walkJSON(dir, skipSubdir, idPrefix string) ([]schemaFile, error) {
	var out []schemaFile
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipSubdir != "" && d.Name() == skipSubdir && filepath.Dir(path) == dir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		out = append(out, schemaFile{abs: path, rel: rel, id: idPrefix + rel})
		return nil
	})
	return out, err
}
