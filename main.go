package main

import (
	"bufio"
	"context"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/olivere/elastic/v7"
	"gopkg.in/yaml.v2"
)

type Indices string

const CodesearchIndex Indices = "codesearch"

type Repo struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

type Config struct {
	Repos []Repo `yaml:"repos"`
}

type CodeLine struct {
	Repository string `json:"repository"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line"`
	Content    string `json:"content"`
	Function   string `json:"function,omitempty"`
}

func readYAML(file string) (Config, error) {
	var config Config
	data, err := os.ReadFile(file)
	if err != nil {
		return config, err
	}
	err = yaml.Unmarshal(data, &config)

	return config, nil
}

func cloneOrUpdate(repo Repo, basePath string) (string, error) {
	targetPath := filepath.Join(basePath, repo.Name)
	if _, err := os.Stat(targetPath); os.IsNotExist(err) {
		_, err := git.PlainClone(targetPath, false, &git.CloneOptions{
			URL: repo.URL,
		})
		return targetPath, err
	} else {
		r, err := git.PlainOpen(targetPath)
		if err != nil {
			return "", err
		}
		w, err := r.Worktree()
		if err != nil {
			return "", err
		}
		err = w.Pull(&git.PullOptions{RemoteName: "origin"})
		if err != nil && err.Error() != "already up-to-date" {
			log.Println("pull warning:", err)
		}
		return targetPath, nil
	}
}

func supportedFileTypes(path string) bool {
	extensions := []string{
		".asm", ".bat", ".bash", ".c", ".cc", ".cfg", ".clj", ".cljc", ".cljs", ".cmd",
		".conf", ".cpp", ".cjs", ".cxx", ".dart", ".dockerfile", ".editorconfig", ".ejs",
		".env", ".env.example", ".erb", ".erl", ".ex", ".exs", ".feature", ".go", ".gradle",
		".groovy", ".h", ".hbs", ".hcl", ".hpp", ".hrl", ".htm", ".html", ".ini", ".java",
		".js", ".json", ".jsonc", ".jsx", ".ksh", ".kt", ".kts", ".less", ".lisp", ".lsp",
		".m", ".make", ".markdown", ".md", ".mk", ".mm", ".mjs", ".mustache", ".nomad",
		".php", ".php5", ".plist", ".properties", ".ps1", ".psql", ".py", ".pyi", ".pyx",
		".rb", ".rs", ".rst", ".s", ".sass", ".scala", ".scss", ".sh", ".spec.js",
		".spec.ts", ".sql", ".swift", ".test.go", ".test.js", ".test.ts", ".tf", ".tfvars",
		".toml", ".ts", ".tsx", ".tsv", ".twig", ".tsx", ".txt", ".xhtml", ".xml", ".yaml",
		".yml", ".zsh", "Dockerfile", "Makefile", ".gitignore", ".gitattributes", ".terraform",
	}

	for _, ext := range extensions {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}

	return false
}

var functionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*func\s+(\([^)]+\)\s*)?(\w+)\s*\(`),                               // Golang
	regexp.MustCompile(`^\s*def\s+(\w+)\s*\(`),                                               // Python
	regexp.MustCompile(`^\s*function\s+(\w+)\s*\(`),                                          // Js
	regexp.MustCompile(`^\s*(?:const|let|var)\s+(\w+)\s*=\s*\(.*?\)\s*=>`),                   // Js
	regexp.MustCompile(`^\s*(\w+)\s*=\s*function\s*\(`),                                      // Js
	regexp.MustCompile(`^\s*(public|private|protected)?\s*function\s+(\w+)\s*\(`),            // PHP
	regexp.MustCompile(`^\s*(public|private|protected)?\s*(static\s+)?[\w<>]+\s+(\w+)\s*\(`), // Java, Kotlin
	regexp.MustCompile(`^\s*[\w\*\s]+\s+(\w+)\s*\(.*\)\s*\{?`),                               // C, C++
	regexp.MustCompile(`^\s*def\s+(\w+)`),                                                    // Ruby
	regexp.MustCompile(`^\s*fn\s+(\w+)`),                                                     // Dart, Rust
	regexp.MustCompile(`^\s*func\s+(\w+)`),                                                   // Swift
	regexp.MustCompile(`^\s*def\s+(\w+)\s*\(`),                                               // Scala
}

func extractFunctionName(line string) string {
	trimmed := strings.TrimSpace(line)
	for _, pattern := range functionPatterns {
		matches := pattern.FindStringSubmatch(trimmed)
		if matches != nil {
			return matches[len(matches)-1]
		}
	}

	return ""
}

func indexCode(es *elastic.Client, repoUrl, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !supportedFileTypes(path) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			functionName := extractFunctionName(line)
			doc := CodeLine{
				Repository: repoUrl,
				FilePath:   path,
				Line:       lineNum,
				Content:    line,
				Function:   functionName,
			}
			_, err := es.Index().
				Index(string(CodesearchIndex)).
				BodyJson(doc).
				Do(context.Background())
			if err != nil {
				log.Println("indexing error:", err)
			}
		}
		return nil
	})
}

func main() {
	config, err := readYAML("repos.yaml")
	if err != nil {
		log.Fatal(err)
	}

	es, err := elastic.NewClient(elastic.SetURL("http://localhost:9200"))
	if err != nil {
		log.Fatal(err)
	}

	basePath := "cloned_repos"
	err = os.MkdirAll(basePath, 0755)
	if err != nil {
		return
	}

	exists, err := es.IndexExists(string(CodesearchIndex)).Do(context.Background())
	if err != nil {
		log.Fatalf("error checking if index exists: %v", err)
	}
	if exists {
		_, err := es.DeleteIndex(string(CodesearchIndex)).Do(context.Background())
		if err != nil {
			log.Fatalf("failed to delete existing index: %v", err)
		}
		log.Println("deleted existing index:", string(CodesearchIndex))
	}

	for _, repo := range config.Repos {
		log.Println("processing repo:", repo.Name)
		localPath, err := cloneOrUpdate(repo, basePath)
		if err != nil {
			log.Println("repo processing error:", err)
			continue
		}
		err = indexCode(es, repo.URL, localPath)
		if err != nil {
			log.Println("indexing error:", err)
		}
	}
}
