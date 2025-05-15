package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
	"os"
)

// Encryption представляет сервис шифрования
type Encryption struct {
	key []byte
}

// NewEncryption создает новый сервис шифрования
func NewEncryption(key []byte) (*Encryption, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes")
	}
	return &Encryption{key: key}, nil
}

// GenerateKey генерирует новый ключ шифрования
func GenerateKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	return key, nil
}

// EncryptFile шифрует файл
func (e *Encryption) EncryptFile(inputPath, outputPath string) error {
	fmt.Printf("Encrypting file from %s to %s\n", inputPath, outputPath)

	// Открываем входной файл
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer input.Close()

	// Создаем выходной файл
	output, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer output.Close()

	// Создаем AES шифр
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Генерируем IV
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return fmt.Errorf("failed to generate IV: %w", err)
	}

	// Записываем IV в начало файла
	if _, err := output.Write(iv); err != nil {
		return fmt.Errorf("failed to write IV: %w", err)
	}

	// Создаем stream шифр
	stream := cipher.NewCFBEncrypter(block, iv)

	// Шифруем файл
	writer := &cipher.StreamWriter{S: stream, W: output}
	written, err := io.Copy(writer, input)
	if err != nil {
		return fmt.Errorf("failed to encrypt file: %w", err)
	}

	fmt.Printf("Successfully encrypted %d bytes\n", written)
	return nil
}

// DecryptFile расшифровывает файл
func (e *Encryption) DecryptFile(inputPath, outputPath string) error {
	fmt.Printf("Decrypting file from %s to %s\n", inputPath, outputPath)

	// Открываем входной файл
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer input.Close()

	// Создаем выходной файл
	output, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer output.Close()

	// Создаем AES шифр
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return fmt.Errorf("failed to create cipher: %w", err)
	}

	// Читаем IV из начала файла
	iv := make([]byte, aes.BlockSize)
	if _, err := io.ReadFull(input, iv); err != nil {
		return fmt.Errorf("failed to read IV: %w", err)
	}

	// Создаем stream дешифратор
	stream := cipher.NewCFBDecrypter(block, iv)

	// Расшифровываем файл
	reader := &cipher.StreamReader{S: stream, R: input}
	written, err := io.Copy(output, reader)
	if err != nil {
		return fmt.Errorf("failed to decrypt file: %w", err)
	}

	fmt.Printf("Successfully decrypted %d bytes\n", written)
	return nil
} 