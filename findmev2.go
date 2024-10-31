package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const logo = `
    ________           __   __  ___     
   / ____/ /___ _____/ /  /  |/  /____ 
  / /_  / / __ '/ __  /  / /|_/ / __ \
 / __/ / / /_/ / /_/ /  / /  / / /_/ /
/_/   /_/\__,_/\__,_/  /_/  /_/\____/ 
                                v2.1
`

type Config struct {
	extensions []string
	keywords   []string
	output     string
	directory  string
	workers    int
	bufSize    int
	maxSize    int64
	maxDepth   int
}

type FileMatch struct {
	path    string
	matches []LineMatch
}

type LineMatch struct {
	LineNumber int
	Content    string
	Keyword    string
}

type Progress struct {
	totalFiles     uint64
	processedFiles uint64
	matchedFiles   uint64
	matchedLines   uint64
	lastUpdateTime time.Time
	startTime      time.Time
}

var (
	filesChan    chan string
	resultsChan  chan FileMatch
	progress     Progress
	interrupted  bool
	interruptMux sync.Mutex
)

func isInterrupted() bool {
	interruptMux.Lock()
	defer interruptMux.Unlock()
	return interrupted
}

func setInterrupted() {
	interruptMux.Lock()
	defer interruptMux.Unlock()
	interrupted = true
}

func getSystemDrives() []string {
	if runtime.GOOS == "windows" {
		drives := make([]string, 0)
		for _, drive := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
			path := string(drive) + ":\\"
			if _, err := os.Stat(path); err == nil {
				drives = append(drives, path)
			}
		}
		return drives
	}
	return []string{"/"}
}

func main() {
	// 定义命令行参数
	searchType := flag.String("type", "text", "搜索文件类型：text(文本文件)、code(代码文件)、all(所有文件)")
	keywords := flag.String("keyword", "password,账号,密码,用户名", "要搜索的关键字,多个关键字用逗号分隔")
	outputFile := flag.String("out", "findme_result.txt", "结果输出文件路径")
	searchPath := flag.String("path", "", "搜索路径,默认搜索所有盘符")
	workerCount := flag.Int("worker", runtime.NumCPU(), "并行工作协程数量")
	bufferSize := flag.Int("buffer", 4096, "读取缓冲区大小(bytes)")
	maxSize := flag.Int64("maxsize", 100*1024*1024, "最大处理文件大小(bytes)")
	maxDepth := flag.Int("depth", -1, "最大搜索深度,-1表示无限制")

	// 自定义Usage
	flag.Usage = func() {
		fmt.Println(logo)
		fmt.Println("使用说明:")
		fmt.Println("  findme 是一个快速文件内容搜索工具")
		fmt.Println("\n参数说明:")
		flag.PrintDefaults()
		fmt.Println("\n示例:")
		fmt.Println("  搜索所有盘符下的密码、账号、用户名信息:")
		fmt.Println("    findme")
		fmt.Println("  搜索指定目录下的特定关键字:")
		fmt.Println("    findme -path C:\\Projects -keyword api_key,secret -type code")
	}

	flag.Parse()

	// 配置文件扩展名映射
	extensionMap := map[string][]string{
		"text": {".txt", ".log", ".ini", ".conf", ".md", ".csv", ".docx", ".xlsx", ".pptx"},
		"code": {".go", ".java", ".py", ".js", ".php", ".cs", ".cpp", ".c", ".h", ".hpp", ".sql", ".xml", ".yaml", ".yml", ".json"},
		"all":  {""},
	}

	// 初始化配置
	config := Config{
		extensions: extensionMap[*searchType],
		keywords:   strings.Split(*keywords, ","),
		output:     *outputFile,
		workers:    *workerCount,
		bufSize:    *bufferSize,
		maxSize:    *maxSize,
		maxDepth:   *maxDepth,
	}

	// 设置搜索路径
	if *searchPath == "" {
		drives := getSystemDrives()
		config.directory = strings.Join(drives, ",")
	} else {
		config.directory = *searchPath
	}

	// 初始化进度统计
	progress = Progress{
		startTime:      time.Now(),
		lastUpdateTime: time.Now(),
	}

	// 设置中断处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[!] 接收到中断信号,正在停止搜索...")
		setInterrupted()
	}()

	fmt.Println(logo)
	fmt.Println("[+] 开始搜索...")
	fmt.Printf("[+] 搜索路径: %s\n", config.directory)
	fmt.Printf("[+] 搜索关键字: %s\n", strings.Join(config.keywords, ", "))
	fmt.Printf("[+] 文件类型: %s\n", *searchType)

	// 初始化通道
	filesChan = make(chan string, 10000)
	resultsChan = make(chan FileMatch, 1000)

	// 启动进度显示
	go showProgress()

	// 启动结果写入协程
	var wg sync.WaitGroup
	wg.Add(1)
	go writeResults(config.output, &wg)

	// 启动工作协程
	workerWg := &sync.WaitGroup{}
	for i := 0; i < config.workers; i++ {
		workerWg.Add(1)
		go worker(config, workerWg)
	}

	// 文件搜索等待组
	var searchWg sync.WaitGroup
	dirs := strings.Split(config.directory, ",")
	searchWg.Add(len(dirs))

	// 开始搜索文件
	for _, dir := range dirs {
		go func(d string) {
			defer searchWg.Done()
			findFiles(d, config)
		}(dir)
	}

	// 等待所有文件搜索完成
	go func() {
		searchWg.Wait()
		close(filesChan)
	}()

	// 等待所有工作协程完成
	workerWg.Wait()
	close(resultsChan)

	// 等待结果写入完成
	wg.Wait()

	if isInterrupted() {
		fmt.Println("\n[!] 搜索已中断")
	}

	duration := time.Since(progress.startTime)
	fmt.Printf("\n[+] 搜索完成!用时: %v\n", duration)
	fmt.Printf("[+] 处理文件数: %d\n", atomic.LoadUint64(&progress.processedFiles))
	fmt.Printf("[+] 匹配文件数: %d\n", atomic.LoadUint64(&progress.matchedFiles))
	fmt.Printf("[+] 匹配行数: %d\n", atomic.LoadUint64(&progress.matchedLines))
	fmt.Printf("[+] 结果已保存至: %s\n", config.output)
}

func showProgress() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		if isInterrupted() {
			return
		}

		processed := atomic.LoadUint64(&progress.processedFiles)
		matched := atomic.LoadUint64(&progress.matchedFiles)
		lines := atomic.LoadUint64(&progress.matchedLines)
		duration := time.Since(progress.startTime)
		speed := float64(processed) / duration.Seconds()

		fmt.Printf("\r\033[K[+] 进度: 已处理 %d 个文件, 找到 %d 个匹配文件, %d 个匹配行 (%.1f 文件/秒)",
			processed, matched, lines, speed)
	}
}

func findFiles(root string, config Config) {
	currentDepth := 0

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if isInterrupted() {
			return filepath.SkipDir
		}

		if err != nil {
			return filepath.SkipDir
		}

		// 检查搜索深度
		if config.maxDepth >= 0 {
			relPath, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			currentDepth = len(strings.Split(relPath, string(os.PathSeparator)))
			if currentDepth > config.maxDepth {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		// 跳过系统目录和隐藏目录
		if info.IsDir() {
			basename := filepath.Base(path)
			if basename[0] == '.' || // 隐藏目录
				basename == "node_modules" ||
				basename == "vendor" ||
				basename == "$RECYCLE.BIN" ||
				basename == "System Volume Information" {
				return filepath.SkipDir
			}
			return nil
		}

		// 检查文件大小
		if info.Size() > config.maxSize {
			return nil
		}

		// 检查文件类型
		if len(config.extensions) == 0 || hasValidExtension(path, config.extensions) {
			atomic.AddUint64(&progress.totalFiles, 1)
			select {
			case filesChan <- path:
				// 成功发送
			default:
				// 通道已满或关闭,跳过该文件
			}
		}
		return nil
	})
}

func hasValidExtension(path string, extensions []string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, validExt := range extensions {
		if ext == validExt {
			return true
		}
	}
	return false
}
func worker(config Config, wg *sync.WaitGroup) {
	defer wg.Done()

	for path := range filesChan {
		if isInterrupted() {
			return
		}

		if matches := findMatchingLines(path, config.keywords, config.bufSize); len(matches) > 0 {
			atomic.AddUint64(&progress.matchedFiles, 1)
			atomic.AddUint64(&progress.matchedLines, uint64(len(matches)))
			resultsChan <- FileMatch{path: path, matches: matches}
		}
		atomic.AddUint64(&progress.processedFiles, 1)
	}
}

func findMatchingLines(filePath string, keywords []string, bufferSize int) []LineMatch {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	matches := make([]LineMatch, 0)
	reader := bufio.NewReaderSize(file, bufferSize)
	currentLineNumber := 1

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			break
		}

		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				matches = append(matches, LineMatch{
					LineNumber: currentLineNumber,
					Content:    strings.TrimSpace(line),
					Keyword:    keyword,
				})
			}
		}

		if err == io.EOF {
			break
		}
		currentLineNumber++
	}

	return matches
}

func writeResults(outputPath string, wg *sync.WaitGroup) {
	defer wg.Done()

	file, err := os.Create(outputPath)
	if err != nil {
		fmt.Printf("[-] 创建输出文件失败: %v\n", err)
		return
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	// 写入搜索信息头
	fmt.Fprintf(writer, "Find-Me Search Results\n")
	fmt.Fprintf(writer, "Search Time: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintln(writer, strings.Repeat("=", 80))

	for result := range resultsChan {
		if isInterrupted() {
			return
		}

		fmt.Fprintf(writer, "[+] 文件路径: %s\n", result.path)
		fmt.Fprintf(writer, "[=] 匹配行数: %d\n", len(result.matches))
		for _, match := range result.matches {
			fmt.Fprintf(writer, "[~] 第 %d 行 (关键字: %s): %s\n",
				match.LineNumber, match.Keyword, match.Content)
		}
		fmt.Fprintln(writer, strings.Repeat("-", 80))
	}
}
