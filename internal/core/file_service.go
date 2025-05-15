package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/crypto"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/network"
)

// FileService представляет сервис для работы с файлами
type FileService struct {
	node       *network.Node
	encryption *crypto.Encryption
	shareDir   string
}

// NewFileService создает новый сервис для работы с файлами
func NewFileService(node *network.Node, encryption *crypto.Encryption, shareDir string) (*FileService, error) {
	// Создаем директорию для файлов если её нет
	if err := os.MkdirAll(shareDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create share directory: %w", err)
	}

	return &FileService{
		node:       node,
		encryption: encryption,
		shareDir:   shareDir,
	}, nil
}

// ShareFile шарит файл в сети
func (s *FileService) ShareFile(ctx context.Context, filePath string) (string, error) {
	fmt.Printf("Sharing file from: %s\n", filePath)
	fmt.Printf("Share directory: %s\n", s.shareDir)

	// Проверяем существование файла
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return "", fmt.Errorf("file does not exist: %w", err)
	}

	// Вычисляем хеш файла
	fileHash, err := s.calculateFileHash(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to calculate hash: %w", err)
	}

	// Создаем путь для зашифрованного файла
	encryptedPath := filepath.Join(s.shareDir, fileHash+".encrypted")

	// Проверяем, не зашифрован ли файл уже
	if _, err := os.Stat(encryptedPath); os.IsNotExist(err) {
		fmt.Printf("Saving encrypted file to: %s\n", encryptedPath)
		fmt.Printf("Encrypting file from %s to %s\n", filePath, encryptedPath)

		// Шифруем файл
		if err := s.encryption.EncryptFile(filePath, encryptedPath); err != nil {
			return "", fmt.Errorf("failed to encrypt file: %w", err)
		}

		// Проверяем размер зашифрованного файла
		encryptedInfo, err := os.Stat(encryptedPath)
		if err != nil {
			return "", fmt.Errorf("failed to get encrypted file info: %w", err)
		}
		fmt.Printf("Successfully encrypted %d bytes\n", encryptedInfo.Size())
	} else {
		fmt.Printf("Using existing encrypted file: %s\n", encryptedPath)
	}

	// Ждем некоторое время после подключения к бутстрап узлам
	fmt.Println("Waiting for DHT initialization...")
	time.Sleep(5 * time.Second)

	// Публикуем информацию о файле в DHT с несколькими попытками
	var publishErr error
	maxAttempts := 5 // Увеличиваем количество попыток
	for i := 0; i < maxAttempts; i++ {
		// Проверяем количество подключенных пиров
		peerCount := len(s.node.Host().Network().Peers())
		fmt.Printf("Connected peers: %d\n", peerCount)

		if peerCount == 0 {
			fmt.Println("No peers connected, waiting...")
			time.Sleep(2 * time.Second)
			continue
		}

		fmt.Printf("Publishing attempt %d/%d...\n", i+1, maxAttempts)
		if err := s.node.PublishFileInfo(ctx, fileHash); err != nil {
			publishErr = fmt.Errorf("failed to publish file: %w", err)
			fmt.Printf("Failed to publish on attempt %d: %v\n", i+1, err)
			time.Sleep(2 * time.Second)
			continue
		}
		publishErr = nil
		fmt.Printf("Successfully published file on attempt %d\n", i+1)
		break
	}

	if publishErr != nil {
		return "", fmt.Errorf("failed to share file: %w", publishErr)
	}

	return fileHash, nil
}

// DownloadFile скачивает файл из сети
func (s *FileService) DownloadFile(ctx context.Context, hash, outputPath string) error {
	fmt.Printf("Downloading file with hash: %s\n", hash)

	// Проверяем, есть ли файл локально
	localFilePath := filepath.Join(s.shareDir, hash+".encrypted")
	if _, err := os.Stat(localFilePath); err == nil {
		fmt.Printf("File found locally at %s, decrypting...\n", localFilePath)
		// Расшифровываем файл
		if err := s.encryption.DecryptFile(localFilePath, outputPath); err != nil {
			return fmt.Errorf("failed to decrypt local file: %w", err)
		}
		fmt.Printf("Successfully decrypted file to: %s\n", outputPath)
		return nil
	}

	// Ищем провайдеров файла с несколькими попытками
	var providers []peer.ID
	var findErr error
	for i := 0; i < 3; i++ {
		providers, findErr = s.node.FindFileProviders(ctx, hash)
		if findErr == nil && len(providers) > 0 {
			break
		}
		fmt.Printf("Failed to find providers on attempt %d: %v\n", i+1, findErr)
		time.Sleep(2 * time.Second)
	}

	if findErr != nil || len(providers) == 0 {
		return fmt.Errorf("failed to find file providers: %w", findErr)
	}

	// Пробуем скачать файл у каждого провайдера
	var lastErr error
	for _, p := range providers {
		// Создаем временный файл для скачивания
		tempFile, err := os.CreateTemp(s.shareDir, "download-*.encrypted")
		if err != nil {
			lastErr = fmt.Errorf("failed to create temp file: %w", err)
			continue
		}
		tempPath := tempFile.Name()
		tempFile.Close()
		defer os.Remove(tempPath)

		// Запрашиваем файл у пира
		stream, err := s.node.RequestFile(ctx, p, hash)
		if err != nil {
			lastErr = fmt.Errorf("failed to request file from %s: %w", p, err)
			continue
		}
		defer stream.Close()

		// Сохраняем файл
		file, err := os.Create(tempPath)
		if err != nil {
			lastErr = fmt.Errorf("failed to create file: %w", err)
			continue
		}

		if _, err := io.Copy(file, stream); err != nil {
			file.Close()
			lastErr = fmt.Errorf("failed to download file: %w", err)
			continue
		}
		file.Close()

		// Расшифровываем файл
		if err := s.encryption.DecryptFile(tempPath, outputPath); err != nil {
			lastErr = fmt.Errorf("failed to decrypt file: %w", err)
			continue
		}

		fmt.Printf("Successfully downloaded and decrypted file to: %s\n", outputPath)
		return nil
	}

	return fmt.Errorf("failed to download file from any provider: %w", lastErr)
}

// calculateFileHash вычисляет SHA-256 хеш файла
func (s *FileService) calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// publishFile публикует информацию о файле в DHT
func (s *FileService) publishFile(ctx context.Context, hash, filePath string) error {
	return s.node.PublishFileInfo(ctx, hash)
}

// findFile ищет файл в DHT
func (s *FileService) findFile(ctx context.Context, hash string) ([]peer.ID, error) {
	return s.node.FindFileProviders(ctx, hash)
}

// ListFiles возвращает список доступных файлов
func (s *FileService) ListFiles() ([]FileInfo, error) {
	files, err := os.ReadDir(s.shareDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read share directory: %w", err)
	}

	var fileInfos []FileInfo
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		// Пропускаем временные файлы
		if filepath.Ext(file.Name()) == ".tmp" {
			continue
		}

		// Получаем хеш из имени файла
		hash := strings.TrimSuffix(file.Name(), ".encrypted")

		info, err := file.Info()
		if err != nil {
			continue
		}

		fileInfos = append(fileInfos, FileInfo{
			Hash: hash,
			Size: formatSize(info.Size()),
		})
	}

	return fileInfos, nil
}

// FileInfo представляет информацию о файле
type FileInfo struct {
	Hash string
	Size string
}

// formatSize форматирует размер файла в человекочитаемый вид
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
} 