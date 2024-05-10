package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	target = "https://api.sensenova.cn" // 目标域名
	port   int                          // 代理端口
)

var pathMap = map[string]string{
	"/v1/api/chat/completions": "/v1/llm/chat-completions",
	"/v1/api/models":           "/v1/llm/models",
}

func main() {
	// 从命令行参数获取配置文件路径
	flag.IntVar(&port, "port", 9000, "The proxy port.")
	flag.Parse()

	// 打印配置信息
	log.Println("Target domain: ", target)
	log.Println("Proxy port: ", port)

	http.HandleFunc("/", handleRequest)
	err := http.ListenAndServe(":"+strconv.Itoa(port), nil)
	if err != nil {
		panic(err)
	}
}

func isAuthHeaderAkSk(value string) bool {
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	major := value[7:]
	if !strings.Contains(major, "|") {
		return false
	}
	akSk := strings.Split(major, "|")
	return len(akSk) == 2
}

func genJWT(ak, sk string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": ak,
		"exp": time.Now().Add(time.Second * 120).Unix(),
		"nbf": time.Now().Add(-time.Second * 5).Unix(),
	})

	return token.SignedString([]byte(sk))
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	// 过滤无效URL
	_, err := url.Parse(r.URL.String())
	if err != nil {
		log.Println("Error parsing URL: ", err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// 去掉环境前缀（针对腾讯云，如果包含的话，目前我只用到了test和release）
	log.Println("origin path", r.URL.Path)
	newPath := r.URL.Path
	_, ok := pathMap[newPath]
	if ok {
		newPath = pathMap[newPath]
	}

	// 拼接目标URL（带上查询字符串，如果有的话）
	// 如果请求中包含 X-Target-Host 头，则使用该头作为目标域名
	// 优先级 header > args > default
	var targetURL string
	if r.Header.Get("X-Target-Host") != "" {
		targetURL = "https://" + r.Header.Get("X-Target-Host") + newPath
	} else {
		targetURL = target + newPath
	}
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// 创建代理HTTP请求
	log.Println("Proxying request to: ", targetURL)
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		log.Println("Error creating proxy request: ", err.Error())
		http.Error(w, "Error creating proxy request", http.StatusInternalServerError)
		return
	}

	// 将原始请求头复制到新请求中
	for headerKey, headerValues := range r.Header {
		for _, headerValue := range headerValues {
			if headerKey == "Authorization" && isAuthHeaderAkSk(headerValue) {
				akSk := strings.Split(headerValue[7:], "|")
				headerValue, err = genJWT(akSk[0], akSk[1])
				if err != nil {
					log.Println("Error generating JWT: ", err.Error())
					http.Error(w, "Error generating JWT", http.StatusInternalServerError)
					return
				}
			}
			proxyReq.Header.Add(headerKey, headerValue)
		}
	}

	// 默认超时时间设置为300s（应对长上下文）
	client := &http.Client{
		// Timeout: 300 * time.Second,  // 代理不干涉超时逻辑，由客户端自行设置
	}

	// 支持本地测试通过代理请求
	/*if os.Getenv("ENV") == "local" {
		proxyURL, _ := url.Parse(httpProxy) // 本地HTTP代理配置
		client.Transport = &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}*/

	// 发起代理请求
	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Println("Error sending proxy request: ", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 将响应头复制到代理响应头中
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 将响应状态码设置为原始响应状态码
	w.WriteHeader(resp.StatusCode)

	// 将响应实体写入到响应流中（支持流式响应）
	buf := make([]byte, 1024)
	for {
		if n, err := resp.Body.Read(buf); err == io.EOF || n == 0 {
			return
		} else if err != nil {
			log.Println("error while reading respbody: ", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else {
			if _, err = w.Write(buf[:n]); err != nil {
				log.Println("error while writing resp: ", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.(http.Flusher).Flush()
		}
	}
}
