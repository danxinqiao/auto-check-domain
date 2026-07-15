package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

const (
	baseURL   = "https://www.juyu.com/ykj/get_list"
	pushURL   = "https://hpbnvpiosrzhgyxbrwpt.supabase.co/functions/v1/wx-push/domain-check"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
)

// Domain 域名信息结构
type Domain struct {
	Name       string `json:"name"`
	Length     int    `json:"length"`
	RemainDays int    `json:"remain_days"`
	ExpireDate string `json:"expire_date"`
	Price      int    `json:"price"`
}

// PushItem 推送给下游服务的一条记录
type PushItem struct {
	Domain string `json:"domain"`
	Length int    `json:"length"`
	Expire string `json:"expire"`
	Price  int    `json:"price"`
}

// PushRequest 推送请求载荷
type PushRequest struct {
	OpenID string     `json:"openid"`
	Title  string     `json:"title"`
	Data   []PushItem `json:"data"`
}

// FetchResponse 列表接口响应结构
type FetchResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	HTML string `json:"html"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("正在获取第一页数据...")
	html, err := fetchFirstPage()
	if err != nil {
		log.Fatalf("抓取失败: %v", err)
	}

	domains, err := parseDomains(html)
	if err != nil {
		log.Fatalf("解析失败: %v", err)
	}
	log.Printf("解析到 %d 条记录", len(domains))

	top5 := filterTop5(domains)
	log.Printf("筛选后 Top5: %+v", top5)

	if err := sendNotification(top5); err != nil {
		log.Fatalf("推送失败: %v", err)
	}
	log.Println("推送完成")
}

// fetchFirstPage 启动浏览器，通过验证码后获取列表页 HTML。
// 流程：通过验证码建立会话 → 用浏览器 fetch 发起 get_list 请求 → 拦截响应；
// 所有请求都通过浏览器原生网络栈发出，自动携带 cookie，不会被反爬识别。
// 若返回 -401（验证码 cookie 尚未生效），等待后重试即可。
func fetchFirstPage() (string, error) {
	log.Println("正在启动浏览器...")
	browser, page, err := openBrowser()
	if err != nil {
		return "", fmt.Errorf("启动浏览器失败: %w", err)
	}
	defer browser.MustClose()

	// 首次通过验证码
	log.Println("首次通过验证码建立会话...")
	solveCaptcha(page)

	fr, err := getPageInfo(page)
	if err != nil {
		return "", err
	}

	// -401: 验证码 cookie 可能尚未生效，等待后重试
	if fr.Code == -401 {
		log.Printf("返回 -401，等待2秒后重试: %s", fr.Msg)
		time.Sleep(time.Second * 2)
		fr, err = getPageInfo(page)
		if err != nil {
			return "", err
		}
	}

	// 请求过于频繁，等待后重试
	if fr.Code == -429 {
		log.Printf("请求过于频繁，等待5秒后重试: %s", fr.Msg)
		time.Sleep(time.Second * 5)
		fr, err = getPageInfo(page)
		if err != nil {
			return "", err
		}
	}

	if fr.Code != 1 {
		return "", fmt.Errorf("请求失败: %s", fr.Msg)
	}
	return fr.HTML, nil
}

// getPageInfo 在浏览器中通过 fetch API 发起列表请求并返回解析后的结果。
// 请求通过浏览器原生网络栈发出，自动携带 cookie，无需手动拼接。
func getPageInfo(page *rod.Page) (*FetchResponse, error) {
	js := `async () => {
		const form = new URLSearchParams();
		form.set('dqsj_1', '3000');
		form.set('psize', '50');
		form.set('page', '1');
		form.set('jgpx', '3');
		const resp = await fetch('` + baseURL + `', {
			method: 'POST',
			headers: {
				'Content-Type': 'application/x-www-form-urlencoded',
				'X-Requested-With': 'XMLHttpRequest'
			},
			body: form.toString()
		});
		return await resp.text();
	}`

	res, err := page.Eval(js)
	if err != nil {
		return nil, fmt.Errorf("浏览器请求失败: %w", err)
	}

	body := res.Value.Str()
	log.Printf("getPageInfo 响应: %s", body[:min(len(body), 300)])

	var fr FetchResponse
	if err := json.Unmarshal([]byte(body), &fr); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}
	return &fr, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseDomains 从 HTML 中解析域名信息
func parseDomains(html string) ([]Domain, error) {
	// 先找到 tbody 区块
	tbodyStart := strings.Index(html, "<tbody>")
	if tbodyStart == -1 {
		return nil, fmt.Errorf("未找到 <tbody>")
	}
	tbodyEnd := strings.Index(html[tbodyStart:], "</tbody>")
	if tbodyEnd == -1 {
		return nil, fmt.Errorf("未找到 </tbody>")
	}
	tbodyHTML := html[tbodyStart : tbodyStart+tbodyEnd+len("</tbody>")]

	// 提取所有 <tr> 块（按行分割处理，注意嵌套标签较少可直接简单分割）
	trs := splitTags(tbodyHTML, "tr")
	var domains []Domain

	domainHrefRe := regexp.MustCompile(`<a[^>]*class=["']a_ym["'][^>]*>\s*([^<]+)\s*</a>`)
	tdRe := regexp.MustCompile(`<td\b[^>]*>(.*?)</td>`)
	remainDaysRe := regexp.MustCompile(`还剩(\d+)天`)
	dateSpanRe := regexp.MustCompile(`<span\b[^>]*class=["']dqsj["'][^>]*>\s*([^<]+)\s*</span>`)
	priceRe := regexp.MustCompile(`(\d+)`)

	for _, tr := range trs {
		m := domainHrefRe.FindStringSubmatch(tr)
		if m == nil {
			continue
		}
		name := strings.TrimSpace(m[1])

		tdMatches := tdRe.FindAllStringSubmatch(tr, -1)
		if len(tdMatches) < 6 {
			continue
		}

		tdText := func(i int) string {
			// 移除可能的 HTML 标签，保留文本
			s := tdMatches[i][1]
			tagStrip := regexp.MustCompile(`<[^>]+>`)
			return strings.TrimSpace(tagStrip.ReplaceAllString(s, ""))
		}

		length, err := strconv.Atoi(tdText(1))
		if err != nil {
			continue
		}

		remainText := tdText(4)
		remainM := remainDaysRe.FindStringSubmatch(remainText)
		if remainM == nil {
			continue
		}
		remainDays, _ := strconv.Atoi(remainM[1])

		var expireDate string
		if dateM := dateSpanRe.FindStringSubmatch(tdMatches[4][1]); dateM != nil {
			expireDate = strings.TrimSpace(dateM[1])
		} else {
			expireDate = "未知"
		}

		priceText := tdText(5)
		priceM := priceRe.FindStringSubmatch(priceText)
		if priceM == nil {
			continue
		}
		price, _ := strconv.Atoi(priceM[1])

		domains = append(domains, Domain{
			Name:       name,
			Length:     length,
			RemainDays: remainDays,
			ExpireDate: expireDate,
			Price:      price,
		})
	}
	return domains, nil
}

// splitTags 将 HTML 按 tagName 切分成多段（仅处理外层闭合标签，适合简单结构）
func splitTags(html, tagName string) []string {
	var out []string
	openTag := "<" + tagName
	closeTag := "</" + tagName + ">"
	start := 0
	for {
		begin := strings.Index(html[start:], openTag)
		if begin == -1 {
			break
		}
		begin += start
		end := strings.Index(html[begin:], closeTag)
		if end == -1 {
			break
		}
		end += begin + len(closeTag)
		out = append(out, html[begin:end])
		start = end
	}
	return out
}

// filterTop5 筛选并按价格升序取前5
func filterTop5(domains []Domain) []Domain {
	var filtered []Domain
	for _, d := range domains {
		if d.Length < 20 && d.RemainDays > 3000 {
			filtered = append(filtered, d)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Price < filtered[j].Price
	})
	if len(filtered) > 5 {
		filtered = filtered[:5]
	}
	return filtered
}

// sendNotification 将结果推送到目标服务
func sendNotification(domains []Domain) error {
	apiToken := os.Getenv("API_TOKEN")
	if apiToken == "" {
		return fmt.Errorf("环境变量 API_TOKEN 未设置")
	}
	sendKey := os.Getenv("SENDKEY")
	if sendKey == "" {
		return fmt.Errorf("环境变量 SENDKEY 未设置")
	}

	var items []PushItem
	for _, d := range domains {
		items = append(items, PushItem{
			Domain: d.Name,
			Length: d.Length,
			Expire: d.ExpireDate,
			Price:  d.Price,
		})
	}

	payload := PushRequest{
		OpenID: sendKey,
		Title:  "域名到期提醒",
		Data:   items,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", pushURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", apiToken)
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	log.Printf("推送状态码: %d", resp.StatusCode)
	log.Printf("推送响应: %s", string(b))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("推送返回非 200: %d", resp.StatusCode)
	}
	return nil
}
