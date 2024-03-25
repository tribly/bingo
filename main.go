package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gabriel-vasile/mimetype"
)

type Config struct {
	Tokens     []string
	UploadPath string
	Port       int
	Domain     string
	Lifetime   string
}

var conf Config

func loadConfig(p string) {
	path := p
	_, err := toml.DecodeFile(path, &conf)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func checkFileExpiration() {
	for {
		uploadPath := conf.UploadPath
		files, err := os.ReadDir(uploadPath)
		if err != nil {
			fmt.Println("Couldn't not read uploaded dir.")
			fmt.Println(err.Error())
			os.Exit(1)
		}

		leng, _ := time.ParseDuration("-" + conf.Lifetime)
		then := time.Now().Add(leng)

		for _, file := range files {
			info, _ := file.Info()
			// older than duration
			if then.Compare(info.ModTime()) == 1 {
				fmt.Println("Deleting:", info.Name())
				err := os.Remove(uploadPath + file.Name())
				if err != nil {
					fmt.Println("Could not remove file:", uploadPath+file.Name())
					fmt.Println(err.Error())
				}
			}
		}

		// Run every 5 minutes
		time.Sleep(time.Minute * 5)
	}
}

func generateRandomName(n int) string {
	var letters = []rune("abcdefghijklmnopqrstuvwxyz")
	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func checkToken(r *http.Request) bool {
	prefix := "Bearer "
	authHeader := r.Header.Get("Authorization")
	reqToken := strings.TrimPrefix(authHeader, prefix)

	for _, token := range conf.Tokens {
		if reqToken == token {
			return true
		}
	}

	return false
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	uploadPath := conf.UploadPath
	r.ParseMultipartForm(10 << 20)

	if !checkToken(r) {
		fmt.Fprintln(w, "Not authenticated.")
		return
	}

	file, handler, err := r.FormFile("fil")
	if err != nil {
		fmt.Println("Error retrieving file")
		fmt.Println(err)
		return
	}
	defer file.Close()

	fileExtension := filepath.Ext(handler.Filename)
	randomName := generateRandomName(3) + fileExtension

	filebytes, err := io.ReadAll(file)
	if err != nil {
		fmt.Println(err)
	}

	if err := os.WriteFile(uploadPath+randomName, filebytes, 0644); err != nil {
		fmt.Println(err)
		return
	}

	u := conf.Domain + "/"
	fmt.Fprintln(w, u+randomName)
}

func serveSyntax(fileName string, w http.ResponseWriter, r *http.Request) {
	lexer := lexers.Get(fileName)
	if lexer == nil {
		http.ServeFile(w, r, fileName)
		return
	}

	style := styles.Get("autumn")
	if style == nil {
		style = styles.Fallback
	}

	formatter := formatters.Get("html")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	contents, err := os.ReadFile(fileName)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	iterator, err := lexer.Tokenise(nil, string(contents))
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	err = formatter.Format(w, style, iterator)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func serveFiles(w http.ResponseWriter, r *http.Request) {
	uploadPath := conf.UploadPath
	fileName := r.URL.Path[1:]
	fullpath := uploadPath + fileName
	file, err := os.OpenFile(fullpath, os.O_RDONLY, 0644)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(w, "File not found.")
		return
	}
	file.Close()

	mtype, err := mimetype.DetectFile(fullpath)
	if err != nil {
		println(err)
		return
	}

	strings.Split(mtype.String(), "/")
	m := strings.Split(mtype.String(), "/")[0]
	mp := strings.Split(mtype.Parent().String(), "/")[0]
	if m == "text" || mp == "text" {
		serveSyntax(fullpath, w, r)
	} else {
		http.ServeFile(w, r, fullpath)
	}
}

func setupRoutes() {
	http.HandleFunc("/upload", uploadFile)
	http.HandleFunc("/", serveFiles)
	err := http.ListenAndServe(":"+strconv.Itoa(conf.Port), nil)
	if err != nil {
		fmt.Println(err.Error())
	}
}

func main() {
	configPath := flag.String("config", "/etc/bingo/bingo.toml", "Path to config")
	flag.Parse()
	loadConfig(*configPath)
	fmt.Println("Running on port", strconv.Itoa(conf.Port))
	go checkFileExpiration()
	setupRoutes()
}
