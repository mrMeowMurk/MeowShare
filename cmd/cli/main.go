package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/core"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/crypto"
	"github.com/mrMeowMurk/P2P-File-Sharing/internal/network"
)

var (
	shareDir string
	keyFile  string
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory: %v\n", err)
		os.Exit(1)
	}

	defaultShareDir := filepath.Join(homeDir, ".p2p-share")
	defaultKeyFile := filepath.Join(defaultShareDir, "key")

	fmt.Printf("Home directory: %s\n", homeDir)
	fmt.Printf("Share directory: %s\n", defaultShareDir)
	fmt.Printf("Key file: %s\n", defaultKeyFile)

	// Создаем директорию для файлов если её нет
	if err := os.MkdirAll(defaultShareDir, 0755); err != nil {
		fmt.Printf("Error creating share directory: %v\n", err)
		os.Exit(1)
	}

	// Проверяем права доступа
	if err := os.WriteFile(filepath.Join(defaultShareDir, "test.txt"), []byte("test"), 0644); err != nil {
		fmt.Printf("Error testing write permissions: %v\n", err)
		os.Exit(1)
	}
	os.Remove(filepath.Join(defaultShareDir, "test.txt"))

	rootCmd := &cobra.Command{
		Use:   "p2p-share",
		Short: "P2P File Sharing Application",
	}

	rootCmd.PersistentFlags().StringVar(&shareDir, "share-dir", defaultShareDir, "Directory for shared files")
	rootCmd.PersistentFlags().StringVar(&keyFile, "key-file", defaultKeyFile, "File containing encryption key")

	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start P2P node",
		RunE:  runStart,
	}

	shareCmd := &cobra.Command{
		Use:   "share [file]",
		Short: "Share a file",
		Args:  cobra.ExactArgs(1),
		RunE:  runShare,
	}

	downloadCmd := &cobra.Command{
		Use:   "download [hash] [output]",
		Short: "Download a file",
		Args:  cobra.ExactArgs(2),
		RunE:  runDownload,
	}

	rootCmd.AddCommand(startCmd, shareCmd, downloadCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func getEncryptionKey() ([]byte, error) {
	// Создаем директорию если её нет
	if err := os.MkdirAll(filepath.Dir(keyFile), 0755); err != nil {
		return nil, fmt.Errorf("failed to create key directory: %w", err)
	}

	// Проверяем существование ключа
	key, err := os.ReadFile(keyFile)
	if os.IsNotExist(err) {
		// Генерируем новый ключ
		key, err = crypto.GenerateKey()
		if err != nil {
			return nil, fmt.Errorf("failed to generate key: %w", err)
		}

		// Сохраняем ключ
		if err := os.WriteFile(keyFile, key, 0600); err != nil {
			return nil, fmt.Errorf("failed to save key: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to read key: %w", err)
	}

	return key, nil
}

func setupServices() (*network.Node, *crypto.Encryption, *core.FileService, error) {
	// Создаем P2P узел
	ctx := context.Background()
	node, err := network.NewNode(ctx, shareDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create node: %w", err)
	}

	// Получаем ключ шифрования
	key, err := getEncryptionKey()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get encryption key: %w", err)
	}

	// Создаем сервис шифрования
	encryption, err := crypto.NewEncryption(key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create encryption service: %w", err)
	}

	// Создаем сервис работы с файлами
	fileService, err := core.NewFileService(node, encryption, shareDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create file service: %w", err)
	}

	return node, encryption, fileService, nil
}

func runStart(cmd *cobra.Command, args []string) error {
	node, _, _, err := setupServices()
	if err != nil {
		return err
	}

	fmt.Println("Starting P2P node...")
	if err := node.Start(); err != nil {
		return fmt.Errorf("failed to start node: %w", err)
	}

	// Ждем прерывания
	select {}
}

func runShare(cmd *cobra.Command, args []string) error {
	node, _, fileService, err := setupServices()
	if err != nil {
		return err
	}

	if err := node.Start(); err != nil {
		return fmt.Errorf("failed to start node: %w", err)
	}

	filePath := args[0]
	hash, err := fileService.ShareFile(context.Background(), filePath)
	if err != nil {
		return fmt.Errorf("failed to share file: %w", err)
	}

	fmt.Printf("File shared successfully!\nHash: %s\n", hash)
	fmt.Println("Node is running and sharing the file. Press Ctrl+C to stop.")

	// Ждем сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	return nil
}

func runDownload(cmd *cobra.Command, args []string) error {
	node, _, fileService, err := setupServices()
	if err != nil {
		return err
	}

	if err := node.Start(); err != nil {
		return fmt.Errorf("failed to start node: %w", err)
	}

	hash := args[0]
	outputPath := args[1]

	if err := fileService.DownloadFile(context.Background(), hash, outputPath); err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	fmt.Println("File downloaded successfully!")
	return nil
} 