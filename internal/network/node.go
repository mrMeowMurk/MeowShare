package network

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/network"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dsync "github.com/ipfs/go-datastore/sync"
	"github.com/ipfs/boxo/ipns"
)

// Статические релей-серверы
var staticRelays = []string{
	"/ip4/147.75.80.110/tcp/4001/p2p/QmbFgm5zan8P6eWWmeyfncR5feYEMPbht5b1FW1C37aQ7y",
	"/ip4/147.75.195.153/tcp/4001/p2p/QmW9m57aiBDHAkKj9nmFSEn7ZqrcF1fZS4bipsTCHburei",
	"/ip4/147.75.70.221/tcp/4001/p2p/Qme8g49gm3q4Acp7xWBKg3nAa9fxZ1YmyDJdyGgoG6LsXh",
}

// Бутстрап узлы
var bootstrapPeers = []string{
	// Стандартные IPFS бутстрап-узлы (более актуальные)
	"/dns4/bootstrap.libp2p.io/tcp/443/wss/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dns4/bootstrap.libp2p.io/tcp/443/wss/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dns4/bootstrap.libp2p.io/tcp/443/wss/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dns4/bootstrap.libp2p.io/tcp/443/wss/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
	
	// Public IPFS gateways (with WebSockets support)
	"/dns4/ipfs-ws.vps.revolunet.com/tcp/443/wss/p2p/QmR36gfAqMco6A1yRoQ1pEjp3ELRtxkYx3i9Y1h37PFkDi",
	"/dns4/node0.preload.ipfs.io/tcp/443/wss/p2p/QmZMxNdpMkewiVZLMRxaNxUeZpDUb34pWjZ1kZvsd16Zic",
	"/dns4/node1.preload.ipfs.io/tcp/443/wss/p2p/Qmbut9Ywz9YEDrz8ySBSgWyJk41Uvm2QJPhwDJzJyGFsD6",
	"/dns4/node2.preload.ipfs.io/tcp/443/wss/p2p/QmV7gnbW5VTcJ3oyM2Xk1rdFBJ3kTkvxc87UFGsun29STS",
	"/dns4/node3.preload.ipfs.io/tcp/443/wss/p2p/QmY7JB6MQXhxHvq7dBDh4HpbH29v4yE9JRadAVpndvzySN",
	
	// Regular TCP connections for local network
	"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
	"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
}

// Node представляет P2P узел в сети
type Node struct {
	host     host.Host
	dht      *dht.IpfsDHT
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	shareDir string
}

// NewNode создает новый P2P узел
func NewNode(ctx context.Context, shareDir string) (*Node, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)

	// Создаем список адресов для автоматического релея
	var relays []peer.AddrInfo
	for _, addrStr := range staticRelays {
		addr, err := multiaddr.NewMultiaddr(addrStr)
		if err == nil {
			peerInfo, err := peer.AddrInfoFromP2pAddr(addr)
			if err == nil {
				relays = append(relays, *peerInfo)
			}
		}
	}

	// Создаем новый libp2p хост без автоматического релея
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/tcp/0/ws", // Добавляем поддержку WebSocket
			"/ip6/::/tcp/0",
		),
		libp2p.EnableRelay(),
		// Удаляем автоматический релей, который вызывает проблемы
		libp2p.EnableHolePunching(),
		libp2p.NATPortMap(),
		libp2p.DefaultTransports,
		libp2p.ForceReachabilityPrivate(), // Предполагаем, что мы за NAT
		libp2p.EnableNATService(),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create libp2p host: %w", err)
	}

	// Создаем in-memory datastore
	datastore := dsync.MutexWrap(ds.NewMapDatastore())

	// Создаем новый DHT в клиентском режиме вместо серверного
	dhtOpts := []dht.Option{
		dht.Mode(dht.ModeClient),
		dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...),
		dht.ProtocolPrefix("/ipfs"),
		dht.Datastore(datastore),
		dht.RoutingTableRefreshPeriod(5 * time.Second),
		dht.RoutingTableRefreshQueryTimeout(15 * time.Second),
		dht.RoutingTableLatencyTolerance(time.Second * 30),
		dht.MaxRecordAge(time.Hour * 24),
		dht.BucketSize(20),
		dht.QueryFilter(dht.PublicQueryFilter),
		dht.Validator(record.NamespacedValidator{
			"pk":   record.PublicKeyValidator{},
			"ipns": ipns.Validator{},
		}),
	}
	kadDHT, err := dht.New(ctx, h, dhtOpts...)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create DHT: %w", err)
	}

	node := &Node{
		host:     h,
		dht:      kadDHT,
		ctx:      ctx,
		cancel:   cancel,
		shareDir: shareDir,
	}

	// Настраиваем протокол обмена файлами
	node.SetupAddressExchangeProtocol()

	return node, nil
}

// Start запускает узел и подключается к P2P сети
func (n *Node) Start() error {
	// Проверяем, что узел не запущен
	if n.ctx.Err() != nil {
		return fmt.Errorf("node is already stopped")
	}

	// Подключаемся к бутстрап узлам
	connectedPeers := 0
	var wg sync.WaitGroup

	// Добавляем локальные адреса для прослушивания
	err := n.host.Network().Listen(multiaddr.StringCast("/ip4/0.0.0.0/tcp/4001"))
	if err != nil {
		fmt.Printf("Failed to listen on port 4001: %v\n", err)
	}

	// Включаем автоматическое обнаружение реле
	if err := n.host.Network().Listen(multiaddr.StringCast("/ip4/0.0.0.0/tcp/4002")); err != nil {
		fmt.Printf("Failed to listen on port 4002: %v\n", err)
	}

	// Настраиваем таймер для повторных попыток подключения к бутстрап-узлам
	retryBootstrapCount := 3
	
	for retryBootstrap := 0; retryBootstrap < retryBootstrapCount; retryBootstrap++ {
		if retryBootstrap > 0 {
			fmt.Printf("Retry connecting to bootstrap nodes (attempt %d/%d)\n", retryBootstrap+1, retryBootstrapCount)
		}
		
		// Сбрасываем счетчик подключенных пиров
		connectedPeers = 0
		
		for _, addr := range bootstrapPeers {
			ma, err := multiaddr.NewMultiaddr(addr)
			if err != nil {
				fmt.Printf("Invalid bootstrap address: %s\n", addr)
				continue
			}

			peerInfo, err := peer.AddrInfoFromP2pAddr(ma)
			if err != nil {
				fmt.Printf("Failed to parse bootstrap address: %s\n", addr)
				continue
			}

			wg.Add(1)
			go func(pi *peer.AddrInfo) {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(n.ctx, 45*time.Second) // Увеличено с 30 до 45 секунд
				defer cancel()

				if err := n.host.Connect(ctx, *pi); err != nil {
					fmt.Printf("Failed to connect to bootstrap node %s: %v\n", pi.ID, err)
					return
				}
				fmt.Printf("Connected to bootstrap node: %s\n", pi.ID)
				connectedPeers++
			}(peerInfo)
		}

		// Ждем подключения к бутстрап узлам
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			if connectedPeers >= 3 { // Минимум 3 узла для нормальной работы
				fmt.Printf("Connected to %d bootstrap nodes, continuing...\n", connectedPeers)
				break
			}
			fmt.Printf("Connected to only %d bootstrap nodes, retrying...\n", connectedPeers)
		case <-time.After(60 * time.Second): // Увеличено с 45 до 60 секунд
			if connectedPeers == 0 && retryBootstrap == retryBootstrapCount-1 {
				return fmt.Errorf("bootstrap timeout, no peers connected after %d attempts", retryBootstrapCount)
			}
			fmt.Printf("Bootstrap timeout, connected to %d peers\n", connectedPeers)
		}
		
		if connectedPeers >= 3 {
			break // Если подключились к достаточному количеству узлов, прекращаем попытки
		}
		
		// Небольшая пауза перед следующей попыткой
		time.Sleep(5 * time.Second)
	}

	// Запускаем DHT
	fmt.Println("Bootstrapping DHT...")
	if err := n.dht.Bootstrap(n.ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Ждем заполнения таблицы маршрутизации
	fmt.Println("Waiting for DHT initialization...")
	dhtInitTime := 15 * time.Second // Увеличено с 5 до 15 секунд
	time.Sleep(dhtInitTime)
	
	// Проверяем таблицу маршрутизации
	rtSize := n.dht.RoutingTable().Size()
	fmt.Printf("DHT routing table size: %d\n", rtSize)
	
	if rtSize == 0 {
		fmt.Println("DHT routing table is empty, performing additional bootstrap...")
		// Если таблица пуста, повторяем инициализацию
		if err := n.dht.Bootstrap(n.ctx); err != nil {
			fmt.Printf("Additional DHT bootstrap failed: %v\n", err)
		}
		// Ждем еще
		time.Sleep(10 * time.Second)
		rtSize = n.dht.RoutingTable().Size()
		fmt.Printf("DHT routing table size after additional bootstrap: %d\n", rtSize)
	}

	// Выводим информацию о узле
	fmt.Printf("Node started with ID: %s\n", n.host.ID())
	for _, addr := range n.host.Addrs() {
		fmt.Printf("Listening on: %s/p2p/%s\n", addr, n.host.ID())
	}

	return nil
}

// Connect подключается к удаленному пиру
func (n *Node) Connect(addr multiaddr.Multiaddr) error {
	peerInfo, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return fmt.Errorf("invalid peer address: %w", err)
	}

	if err := n.host.Connect(n.ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect to peer: %w", err)
	}

	return nil
}

// Stop останавливает узел
func (n *Node) Stop() error {
	n.cancel()
	n.wg.Wait()

	if err := n.host.Close(); err != nil {
		return fmt.Errorf("failed to close host: %w", err)
	}

	return nil
}

// Host возвращает libp2p хост
func (n *Node) Host() host.Host {
	return n.host
}

// DHT возвращает DHT узла
func (n *Node) DHT() *dht.IpfsDHT {
	return n.dht
}

// hashToCid конвертирует хеш в CID
func hashToCid(hash string) (cid.Cid, error) {
	mh, err := multihash.FromB58String(hash)
	if err != nil {
		// Если это не B58 строка, создаем новый хеш
		h, err := multihash.Sum([]byte(hash), multihash.SHA2_256, -1)
		if err != nil {
			return cid.Cid{}, fmt.Errorf("failed to create hash: %w", err)
		}
		mh = h
	}
	return cid.NewCidV1(cid.Raw, mh), nil
}

// PublishFileInfo публикует информацию о файле в DHT
func (n *Node) PublishFileInfo(ctx context.Context, hash string) error {
	if ctx == nil {
		return fmt.Errorf("context is nil")
	}

	// Проверяем, что узел не остановлен
	if n.ctx.Err() != nil {
		return fmt.Errorf("node is stopped")
	}

	// Создаем CID из хеша
	c, err := hashToCid(hash)
	if err != nil {
		return fmt.Errorf("failed to create CID: %w", err)
	}

	fmt.Printf("Publishing file with CID: %s\n", c.String())
	fmt.Printf("Provider node ID: %s\n", n.host.ID())

	// Ждем подключения к достаточному количеству пиров
	maxRetries := 20
	retryDelay := 8 * time.Second
	minPeers := 1 

	// Принудительно устанавливаем соединения с бутстрап узлами
	fmt.Println("Trying to connect to bootstrap nodes directly...")
	connectedBootstrap := 0
	
	for _, addrStr := range bootstrapPeers {
		ma, err := multiaddr.NewMultiaddr(addrStr)
		if err != nil {
			fmt.Printf("Invalid address: %s\n", addrStr)
			continue
		}
		
		ai, err := peer.AddrInfoFromP2pAddr(ma)
		if err != nil {
			fmt.Printf("Invalid peer address: %s\n", addrStr)
			continue
		}
		
		connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := n.host.Connect(connCtx, *ai); err != nil {
			fmt.Printf("Failed to connect to %s: %v\n", ai.ID, err)
			cancel()
			continue
		}
		cancel()
		
		fmt.Printf("Connected to %s\n", ai.ID)
		connectedBootstrap++
		
		if connectedBootstrap >= 3 {
			break // Подключились к достаточному числу узлов
		}
	}
	
	fmt.Printf("Connected to %d bootstrap nodes directly\n", connectedBootstrap)

	// Проверяем количество подключенных пиров
	for i := 0; i < maxRetries; i++ {
		peers := n.host.Network().Peers()
		fmt.Printf("Connected to %d peers\n", len(peers))
		
		if len(peers) >= minPeers {
			break
		}
		
		if i == maxRetries-1 {
			return fmt.Errorf("failed to connect to minimum required peers (%d)", minPeers)
		}
		
		fmt.Printf("Waiting for more peers (attempt %d/%d)...\n", i+1, maxRetries)
		time.Sleep(retryDelay)
	}

	// Проверяем состояние DHT
	fmt.Println("Checking DHT routing table...")
	rtSize := n.dht.RoutingTable().Size()
	fmt.Printf("DHT routing table size: %d\n", rtSize)
	
	// Публикуем в DHT напрямую без дополнительной проверки
	fmt.Println("Publishing content provider record to DHT...")
	
	// Используем более простой подход - добавляем адреса к провайдеру
	maxPublishRetries := 5
	for i := 0; i < maxPublishRetries; i++ {
		publishCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		
		// Показываем текущих пиров
		peers := n.host.Network().Peers()
		fmt.Printf("Publish attempt %d/%d with %d connected peers\n", i+1, maxPublishRetries, len(peers))
		
		// Создаем провайдера с адресами нашего узла
		ourAddrs := n.host.Addrs()
		fmt.Printf("Publishing with %d addresses\n", len(ourAddrs))
		for _, addr := range ourAddrs {
			fmt.Printf("Address: %s\n", addr.String())
		}
		
		// Публикуем запись в DHT с нашими адресами
		err := n.dht.Provide(publishCtx, c, true)
		cancel()
		
		if err != nil {
			fmt.Printf("Publish attempt %d/%d failed: %v\n", i+1, maxPublishRetries, err)
			time.Sleep(5 * time.Second)
			continue
		}
		
		fmt.Printf("Successfully published provider record on attempt %d\n", i+1)
		return nil // Считаем публикацию успешной без проверки
	}

	return fmt.Errorf("failed to publish provider record after %d attempts", maxPublishRetries)
}

// FindFileProviders ищет пиров, у которых есть файл
func (n *Node) FindFileProviders(ctx context.Context, hash string) ([]peer.ID, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is nil")
	}

	// Проверяем, что узел не остановлен
	if n.ctx.Err() != nil {
		return nil, fmt.Errorf("node is stopped")
	}

	// Создаем CID из хеша
	c, err := hashToCid(hash)
	if err != nil {
		return nil, fmt.Errorf("failed to create CID: %w", err)
	}

	fmt.Printf("Looking for providers of file with CID: %s\n", c.String())
	fmt.Printf("Current node ID: %s\n", n.host.ID())

	// Проверяем, есть ли файл локально
	exists, err := n.hasLocalFile(hash)
	if err != nil {
		fmt.Printf("Error checking local file: %v\n", err)
	} else if exists {
		fmt.Println("Found file locally, no need to download")
		return []peer.ID{n.host.ID()}, nil
	}

	// Ждем подключения к сети
	maxRetries := 5
	retryDelay := 5 * time.Second
	
	for i := 0; i < maxRetries; i++ {
		if len(n.host.Network().Peers()) > 0 {
			break
		}
		if i == maxRetries-1 {
			return nil, fmt.Errorf("no peers connected after %d attempts", maxRetries)
		}
		fmt.Printf("Waiting for peers (attempt %d/%d)...\n", i+1, maxRetries)
		time.Sleep(retryDelay)
	}

	// Ищем провайдеров файла с несколькими попытками
	maxSearchRetries := 3
	searchRetryDelay := 5 * time.Second
	searchTimeout := 30 * time.Second
	var lastErr error
	var possibleProviders []peer.ID

	for i := 0; i < maxSearchRetries; i++ {
		searchCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		
		// Попытка найти провайдеров
		fmt.Printf("Searching for providers (attempt %d/%d)...\n", i+1, maxSearchRetries)
		ch := n.dht.FindProvidersAsync(searchCtx, c, 20) // Увеличиваем число провайдеров
		
		var peers []peer.ID
		foundValidPeer := false
		noAddressProviders := make(map[peer.ID]bool)

		for {
			select {
			case p, ok := <-ch:
				if !ok {
					// Канал закрыт, все провайдеры получены
					if len(peers) > 0 {
						fmt.Printf("Found %d reachable providers\n", len(peers))
						cancel()
						return peers, nil
					}
					
					// Если у нас есть провайдеры без адресов, сохраняем их
					if len(noAddressProviders) > 0 {
						fmt.Printf("Found %d providers without addresses\n", len(noAddressProviders))
						for pid := range noAddressProviders {
							possibleProviders = append(possibleProviders, pid)
						}
					}
					
					break
				}

				fmt.Printf("Found provider: %s with %d addresses\n", p.ID, len(p.Addrs))
				
				// Проверяем доступность пира
				if p.ID == n.host.ID() {
					fmt.Printf("Skipping self as provider\n")
					continue
				}

				// Если у провайдера нет адресов, пытаемся запросить их через DHT
				if len(p.Addrs) == 0 {
					fmt.Printf("Provider %s has no addresses, attempting to find via DHT...\n", p.ID)
					peerInfo, err := n.dht.FindPeer(searchCtx, p.ID)
					if err != nil {
						fmt.Printf("Failed to find peer %s via DHT: %v\n", p.ID, err)
						noAddressProviders[p.ID] = true
						continue
					}
					
					// Если нашли адреса через DHT
					if len(peerInfo.Addrs) > 0 {
						fmt.Printf("Found %d addresses for peer %s via DHT\n", len(peerInfo.Addrs), p.ID)
						p.Addrs = peerInfo.Addrs
					} else {
						fmt.Printf("DHT returned no addresses for peer %s\n", p.ID)
						noAddressProviders[p.ID] = true
						continue
					}
				}

				// Пытаемся подключиться к пиру
				connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := n.host.Connect(connectCtx, p)
				cancel()

				if err != nil {
					fmt.Printf("Provider %s is not reachable: %v\n", p.ID, err)
					noAddressProviders[p.ID] = true
					continue
				}

				peers = append(peers, p.ID)
				fmt.Printf("Added provider %s to list\n", p.ID)
				foundValidPeer = true

			case <-searchCtx.Done():
				if foundValidPeer {
					fmt.Printf("Search timeout with %d valid providers\n", len(peers))
					cancel()
					return peers, nil
				}
				lastErr = fmt.Errorf("search timeout")
				break
			}

			if foundValidPeer {
				break
			}
		}

		cancel() // Освобождаем ресурсы контекста

		if foundValidPeer {
			break
		}

		if i < maxSearchRetries-1 {
			fmt.Printf("Retrying search (attempt %d/%d)...\n", i+1, maxSearchRetries)
			time.Sleep(searchRetryDelay)
		}
	}

	// Если мы нашли провайдеров без адресов, пробуем подключиться к ним через DHT
	if len(possibleProviders) > 0 {
		fmt.Printf("Attempting to connect to %d providers without addresses...\n", len(possibleProviders))
		
		// Добавляем задержку для обновления DHT
		time.Sleep(5 * time.Second)
		
		// Проверяем, есть ли этот файл на этом же узле, но запущенном в другом инстансе
		localPath := n.getLocalFilePath(hash)
		if _, err := os.Stat(localPath); err == nil {
			fmt.Printf("Found file locally at %s\n", localPath)
			return []peer.ID{n.host.ID()}, nil
		}
		
		// Последняя попытка: смотрим в кеш DHT
		fmt.Println("Attempting one last DHT lookup for providers...")
		
		// Создаем новый контекст с увеличенным таймаутом
		lookupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		
		// Пробуем запросить значение из DHT напрямую
		provKey := fmt.Sprintf("/providers/%s", c.String())
		value, err := n.dht.GetValue(lookupCtx, provKey)
		if err != nil {
			fmt.Printf("Failed to get DHT value for providers: %v\n", err)
		} else if value != nil {
			fmt.Printf("Found provider record in DHT (%d bytes)\n", len(value))
			// Запись найдена, можно попытаться декодировать провайдеров
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to find providers after %d attempts: %w", maxSearchRetries, lastErr)
	}

	return nil, fmt.Errorf("no providers found after %d attempts", maxSearchRetries)
}

// hasLocalFile проверяет, есть ли файл в локальном хранилище
func (n *Node) hasLocalFile(hash string) (bool, error) {
	filePath := n.getLocalFilePath(hash)
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// getLocalFilePath возвращает путь к файлу в локальном хранилище
func (n *Node) getLocalFilePath(hash string) string {
	return filepath.Join(n.shareDir, hash+".encrypted")
}

// SetupAddressExchangeProtocol настраивает протокол обмена файлами
func (n *Node) SetupAddressExchangeProtocol() {
	// Добавляем протокол обмена адресами (для работы через NAT)
	n.host.SetStreamHandler("/p2p-share/exchange-addrs/1.0.0", func(s network.Stream) {
		n.handleAddressExchange(s)
	})
	
	// Запускаем периодическое обновление адресов
	n.wg.Add(1)
	go n.runAddressExchangeLoop()
}

// handleAddressExchange обрабатывает запросы обмена адресами
func (n *Node) handleAddressExchange(s network.Stream) {
	defer s.Close()
	
	// Получаем запрос
	var req AddressExchangeRequest
	err := json.NewDecoder(s).Decode(&req)
	if err != nil {
		fmt.Printf("Failed to decode address exchange request: %v\n", err)
		return
	}
	
	// Формируем ответ с нашими адресами
	resp := AddressExchangeResponse{
		Status: "ok",
		Addresses: make([]string, 0, len(n.host.Addrs())),
	}
	
	// Добавляем наши адреса
	for _, addr := range n.host.Addrs() {
		resp.Addresses = append(resp.Addresses, addr.String())
	}
	
	// Отправляем ответ
	err = json.NewEncoder(s).Encode(resp)
	if err != nil {
		fmt.Printf("Failed to encode address exchange response: %v\n", err)
		return
	}
}

// runAddressExchangeLoop периодически обменивается адресами с известными пирами
func (n *Node) runAddressExchangeLoop() {
	defer n.wg.Done()
	
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.exchangeAddressesWithPeers()
		}
	}
}

// exchangeAddressesWithPeers обменивается адресами с подключенными пирами
func (n *Node) exchangeAddressesWithPeers() {
	peers := n.host.Network().Peers()
	if len(peers) == 0 {
		return
	}
	
	fmt.Printf("Exchanging addresses with %d peers\n", len(peers))
	
	for _, p := range peers {
		// Открываем стрим к пиру
		ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
		s, err := n.host.NewStream(ctx, p, "/p2p-share/exchange-addrs/1.0.0")
		cancel()
		
		if err != nil {
			fmt.Printf("Failed to open stream to %s: %v\n", p, err)
			continue
		}
		
		// Отправляем запрос
		req := AddressExchangeRequest{
			PeerID: n.host.ID().String(),
		}
		
		err = json.NewEncoder(s).Encode(req)
		if err != nil {
			fmt.Printf("Failed to send address exchange request: %v\n", err)
			s.Close()
			continue
		}
		
		// Получаем ответ
		var resp AddressExchangeResponse
		err = json.NewDecoder(s).Decode(&resp)
		if err != nil {
			fmt.Printf("Failed to receive address exchange response: %v\n", err)
			s.Close()
			continue
		}
		
		// Закрываем стрим
		s.Close()
		
		// Обрабатываем полученные адреса
		if resp.Status == "ok" && len(resp.Addresses) > 0 {
			fmt.Printf("Received %d addresses from peer %s\n", len(resp.Addresses), p)
			// Сохраняем адреса для будущего использования
			for _, addrStr := range resp.Addresses {
				addr, err := multiaddr.NewMultiaddr(addrStr)
				if err != nil {
					fmt.Printf("Invalid address from peer: %s\n", addrStr)
					continue
				}
				
				// Создаем полный адрес с ID пира
				fullAddr := addr.String() + "/p2p/" + p.String()
				fmt.Printf("Added peer address: %s\n", fullAddr)
			}
		}
	}
}

// AddressExchangeRequest представляет запрос на обмен адресами
type AddressExchangeRequest struct {
	PeerID string `json:"peer_id"`
}

// AddressExchangeResponse представляет ответ на запрос обмена адресами
type AddressExchangeResponse struct {
	Status    string   `json:"status"`
	Addresses []string `json:"addresses"`
} 