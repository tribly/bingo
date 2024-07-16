package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
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

	"github.com/gofiber/fiber/v2"
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

func checkToken(ctx *fiber.Ctx) bool {
	prefix := "Bearer "
	authHeader := ctx.Get("Authorization")
	reqToken := strings.TrimPrefix(authHeader, prefix)

	for _, token := range conf.Tokens {
		if reqToken == token {
			return true
		}
	}

	return false
}

func uploadFile(ctx *fiber.Ctx) error {
	if !checkToken(ctx) {
		return ctx.SendString("Not authenticated.")
	}

	uploadPath := conf.UploadPath
	form, err := ctx.MultipartForm()
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	files := form.File["documents"]

	for _, file := range files {
		fmt.Println(file.Filename, file.Size, file.Header["Content-Type"][0])

		fileExtension := filepath.Ext(file.Filename)
		randomName := generateRandomName(3) + fileExtension

		ctx.SaveFile(file, uploadPath+randomName)

		u := conf.Domain + "/"
		return ctx.SendString(u + randomName)
	}

	return err
}

func serveSyntax(fileName string) io.Reader {
	lexer := lexers.Get(fileName)
	if lexer == nil {
		f, _ := os.Open(fileName)
		return f
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

	formatted := new(bytes.Buffer)
	err = formatter.Format(formatted, style, iterator)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	return formatted
}

func serveFiles(ctx *fiber.Ctx) error {
	uploadPath := conf.UploadPath
	fileName := ctx.Params("filename")
	fullpath := uploadPath + fileName
	file, err := os.OpenFile(fullpath, os.O_RDONLY, 0644)
	if errors.Is(err, os.ErrNotExist) {
		return ctx.SendString("File not found.")
	}
	file.Close()

	mtype, err := mimetype.DetectFile(fullpath)
	if err != nil {
		return ctx.SendString(err.Error())
	}

	strings.Split(mtype.String(), "/")
	m := strings.Split(mtype.String(), "/")[0]
	mp := strings.Split(mtype.Parent().String(), "/")[0]
	if m == "text" || mp == "text" {
		formatted := serveSyntax(fullpath)
		ctx.Set(fiber.HeaderContentType, fiber.MIMETextHTML)
		return ctx.SendStream(formatted)
	}

	return ctx.SendFile(fullpath, true)
}

func setupRoutes() {
	app := fiber.New()
	app.Get("/:filename", serveFiles)
	app.Post("/", uploadFile)
	app.Listen(":" + strconv.Itoa(conf.Port))
}

func main() {
	configPath := flag.String("config", "/etc/bingo/bingo.toml", "Path to config")
	flag.Parse()
	loadConfig(*configPath)
	fmt.Println("Running on port", strconv.Itoa(conf.Port))
	go checkFileExpiration()
	setupRoutes()
}
