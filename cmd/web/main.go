package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/core"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/crypto"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/network"
)

type WebServer struct {
	node        *network.Node
	fileService *core.FileService
	templates   *template.Template
}

type PageData struct {
	NodeID    string
	PeerCount int
	Files     []core.FileInfo
}

func main() {
	// Получаем домашнюю директорию
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory: %v\n", err)
		os.Exit(1)
	}

	// Настраиваем пути
	shareDir := filepath.Join(homeDir, ".p2p-share")
	keyFile := filepath.Join(shareDir, "key")

	// Создаем P2P узел
	node, err := network.NewNode(context.Background(), shareDir)
	if err != nil {
		fmt.Printf("Error creating node: %v\n", err)
		os.Exit(1)
	}

	// Получаем ключ шифрования
	key, err := os.ReadFile(keyFile)
	if os.IsNotExist(err) {
		key, err = crypto.GenerateKey()
		if err != nil {
			fmt.Printf("Error generating key: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(keyFile, key, 0600); err != nil {
			fmt.Printf("Error saving key: %v\n", err)
			os.Exit(1)
		}
	} else if err != nil {
		fmt.Printf("Error reading key: %v\n", err)
		os.Exit(1)
	}

	// Создаем сервис шифрования
	encryption, err := crypto.NewEncryption(key)
	if err != nil {
		fmt.Printf("Error creating encryption service: %v\n", err)
		os.Exit(1)
	}

	// Создаем сервис работы с файлами
	fileService, err := core.NewFileService(node, encryption, shareDir)
	if err != nil {
		fmt.Printf("Error creating file service: %v\n", err)
		os.Exit(1)
	}

	// Запускаем P2P узел
	if err := node.Start(); err != nil {
		fmt.Printf("Error starting node: %v\n", err)
		os.Exit(1)
	}

	// Создаем веб-сервер
	server := &WebServer{
		node:        node,
		fileService: fileService,
		templates:   template.Must(template.ParseGlob("web/templates/*.html")),
	}

	// Настраиваем маршруты
	r := gin.Default()
	r.LoadHTMLGlob("web/templates/*")
	r.GET("/", server.handleIndex)
	r.POST("/upload", server.handleUpload)
	r.POST("/download", server.handleDownload)
	r.GET("/download/:hash", server.handleDownloadFile)

	// Запускаем веб-сервер
	fmt.Println("Web server starting on http://localhost:8080")
	if err := r.Run(":8080"); err != nil {
		fmt.Printf("Error starting web server: %v\n", err)
		os.Exit(1)
	}
}

func (s *WebServer) handleIndex(c *gin.Context) {
	// Получаем список файлов
	files, err := s.fileService.ListFiles()
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error getting file list: %v", err))
		return
	}

	data := PageData{
		NodeID:    s.node.Host().ID().String(),
		PeerCount: len(s.node.Host().Network().Peers()),
		Files:     files,
	}

	c.HTML(http.StatusOK, "index.html", data)
}

func (s *WebServer) handleUpload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.String(http.StatusBadRequest, fmt.Sprintf("Error getting file: %v", err))
		return
	}

	// Сохраняем файл временно
	tempPath := filepath.Join(os.TempDir(), file.Filename)
	if err := c.SaveUploadedFile(file, tempPath); err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error saving file: %v", err))
		return
	}
	defer os.Remove(tempPath)

	// Делимся файлом
	fileHash, err := s.fileService.ShareFile(c.Request.Context(), tempPath)
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error sharing file: %v", err))
		return
	}

	// Добавляем хеш файла в параметры редиректа
	c.Redirect(http.StatusFound, fmt.Sprintf("/?uploaded=%s", fileHash))
}

func (s *WebServer) handleDownload(c *gin.Context) {
	fileHash := c.PostForm("hash")
	if fileHash == "" {
		c.String(http.StatusBadRequest, "Hash is required")
		return
	}

	c.Redirect(http.StatusFound, "/download/"+fileHash)
}

func (s *WebServer) handleDownloadFile(c *gin.Context) {
	fileHash := c.Param("hash")
	if fileHash == "" {
		c.String(http.StatusBadRequest, "Hash is required")
		return
	}

	// Создаем временный файл для скачивания
	tempFile, err := os.CreateTemp("", "download-*")
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error creating temp file: %v", err))
		return
	}
	defer os.Remove(tempFile.Name())
	tempFile.Close()

	// Скачиваем файл
	if err := s.fileService.DownloadFile(c.Request.Context(), fileHash, tempFile.Name()); err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error downloading file: %v", err))
		return
	}

	// Отправляем файл пользователю
	c.File(tempFile.Name())
} 