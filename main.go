package main

import (
	"bufio"
	"bytes"
	"embed"
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

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/gofiber/template/html/v2"
)

type Config struct {
	Tokens     []string
	UploadPath string
	Port       int
	Domain     string
	Lifetime   string
}

type File struct {
	Name string
}

type Multi struct {
	Files []File
}

var conf Config

//go:embed views/*
var views embed.FS

//go:embed views/multipaste.html
var multiview embed.FS

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

func checkToken(token string) bool {
	for _, allowed_token := range conf.Tokens {
		if token == allowed_token {
			return true
		}
	}

	return false
}

func uploadFile(ctx *fiber.Ctx) error {
	form, err := ctx.MultipartForm()
	token := form.Value["token"][0] // TODO out of range exception if empty

	if !checkToken(token) {
		return ctx.SendString("Not authenticated.")
	}

	uploadPath := conf.UploadPath
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	files := form.File["files"]

	if len(files) == 1 {
		fileExtension := filepath.Ext(files[0].Filename)
		randomName := generateRandomName(3) + fileExtension

		ctx.SaveFile(files[0], uploadPath+randomName)

		if ctx.Get("User-Agent") == "dingo_client" {
			u := conf.Domain + "/"
			return ctx.SendString(u + randomName)
		}

		return ctx.Redirect("/" + randomName)
	}

	var multi_upload []string

	for _, file := range files {
		fileExtension := filepath.Ext(file.Filename)
		randomName := generateRandomName(3) + fileExtension

		ctx.SaveFile(file, uploadPath+randomName)
		multi_upload = append(multi_upload, randomName)
	}

	multi_name := "m-" + generateRandomName(3)
	f, err := os.Create(uploadPath + multi_name)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, line := range multi_upload {
		_, err := f.WriteString(line + "\n")
		if err != nil {
			return err
		}
	}

	return ctx.Redirect("/" + multi_name)
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

func serve(ctx *fiber.Ctx) error {
	uploadPath := conf.UploadPath
	fileName := ctx.Params("filename")
	fullpath := uploadPath + fileName
	file, err := os.OpenFile(fullpath, os.O_RDONLY, 0644)
	if errors.Is(err, os.ErrNotExist) {
		return ctx.SendString("File not found.")
	}
	file.Close()

	if strings.Split(ctx.Params("filename"), "-")[0] == "m" {
		return serveMulti(fullpath, ctx)
	} else {
		return serveSingle(fullpath, ctx)
	}
}

func serveSingle(fullpath string, ctx *fiber.Ctx) error {
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

func serveMulti(fullpath string, ctx *fiber.Ctx) error {
	file, err := os.Open(fullpath)
	if err != nil {
		return ctx.SendString("File not found.")
	}
	defer file.Close()

	fileScanner := bufio.NewScanner(file)

	var multiple_files Multi

	for fileScanner.Scan() {
		multiple_files.Files = append(multiple_files.Files, File{Name: fileScanner.Text()})
	}

	return ctx.Render("views/multipaste", fiber.Map{
		"Files": multiple_files.Files,
	})
}

func setupRoutes() {
	engine := html.NewFileSystem(http.FS(views), ".html")
	app := fiber.New(fiber.Config{
		Views:     engine,
		BodyLimit: 1000 * 1024 * 1024,
	})
	app.Use("/", filesystem.New(filesystem.Config{
		Root:       http.FS(views),
		PathPrefix: "views",
	}))
	app.Static("/u/", "./upload")
	app.Get("/:filename", serve)
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
