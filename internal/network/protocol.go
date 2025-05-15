package network

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	ProtocolID          = "/p2p-file-sharing/1.0.0"
	FileRequestType     = "file-request"
	FileResponseType    = "file-response"
	ChunkSize          = 1024 * 1024 // 1MB
)

type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type FileRequest struct {
	Hash string `json:"hash"`
}

type FileResponse struct {
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Chunks   int    `json:"chunks"`
	Error    string `json:"error,omitempty"`
}

// SetupProtocol настраивает протокол обмена файлами
func (n *Node) SetupProtocol() {
	// Основной протокол обмена файлами
	n.host.SetStreamHandler(protocol.ID(ProtocolID), n.handleStream)
	
	// Настраиваем протокол обмена адресами
	n.SetupAddressExchangeProtocol()
}

// handleStream обрабатывает входящие соединения
func (n *Node) handleStream(s network.Stream) {
	defer s.Close()

	reader := bufio.NewReader(s)
	writer := bufio.NewWriter(s)

	// Читаем сообщение
	msg := &Message{}
	if err := json.NewDecoder(reader).Decode(msg); err != nil {
		fmt.Printf("Error reading message: %v\n", err)
		return
	}

	switch msg.Type {
	case FileRequestType:
		n.handleFileRequest(msg, writer)
	default:
		fmt.Printf("Unknown message type: %s\n", msg.Type)
	}
}

// handleFileRequest обрабатывает запрос на получение файла
func (n *Node) handleFileRequest(msg *Message, writer *bufio.Writer) {
	// Декодируем запрос
	req := &FileRequest{}
	if err := json.Unmarshal(msg.Payload, req); err != nil {
		fmt.Printf("Error decoding file request: %v\n", err)
		return
	}

	// Получаем путь к файлу
	filePath := filepath.Join(n.shareDir, req.Hash+".encrypted")

	// Открываем файл
	file, err := os.Open(filePath)
	if err != nil {
		// Отправляем ошибку
		resp := &FileResponse{
			Hash:  req.Hash,
			Error: fmt.Sprintf("File not found: %v", err),
		}
		if err := json.NewEncoder(writer).Encode(resp); err != nil {
			fmt.Printf("Error sending error response: %v\n", err)
		}
		writer.Flush()
		return
	}
	defer file.Close()

	// Получаем размер файла
	stat, err := file.Stat()
	if err != nil {
		fmt.Printf("Error getting file stats: %v\n", err)
		return
	}

	// Отправляем информацию о файле
	resp := &FileResponse{
		Hash:   req.Hash,
		Size:   stat.Size(),
		Chunks: int(stat.Size()/ChunkSize) + 1,
	}
	if err := json.NewEncoder(writer).Encode(resp); err != nil {
		fmt.Printf("Error sending file response: %v\n", err)
		return
	}
	writer.Flush()

	// Отправляем файл по частям
	buf := make([]byte, ChunkSize)
	for {
		n, err := file.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("Error reading file: %v\n", err)
			return
		}

		if _, err := writer.Write(buf[:n]); err != nil {
			fmt.Printf("Error sending file chunk: %v\n", err)
			return
		}
		if err := writer.Flush(); err != nil {
			fmt.Printf("Error flushing data: %v\n", err)
			return
		}
	}
}

// RequestFile запрашивает файл у пира
func (n *Node) RequestFile(ctx context.Context, p peer.ID, hash string) (io.ReadCloser, error) {
	// Получаем адреса пира через DHT
	peerInfo, err := n.dht.FindPeer(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("failed to find peer addresses: %w", err)
	}

	// Подключаемся к пиру если еще не подключены
	if n.host.Network().Connectedness(p) != network.Connected {
		if err := n.host.Connect(ctx, peerInfo); err != nil {
			return nil, fmt.Errorf("failed to connect to peer: %w", err)
		}
	}

	// Открываем поток к пиру
	s, err := n.host.NewStream(ctx, p, protocol.ID(ProtocolID))
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}

	// Отправляем запрос
	req := &Message{
		Type: FileRequestType,
		Payload: json.RawMessage(fmt.Sprintf(`{
			"hash": "%s"
		}`, hash)),
	}

	writer := bufio.NewWriter(s)
	if err := json.NewEncoder(writer).Encode(req); err != nil {
		s.Close()
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	if err := writer.Flush(); err != nil {
		s.Close()
		return nil, fmt.Errorf("failed to flush request: %w", err)
	}

	// Читаем ответ
	resp := &FileResponse{}
	if err := json.NewDecoder(s).Decode(resp); err != nil {
		s.Close()
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.Error != "" {
		s.Close()
		return nil, fmt.Errorf("peer error: %s", resp.Error)
	}

	return s, nil
} 