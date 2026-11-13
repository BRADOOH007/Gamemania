package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	resetColor   = "\033[0m"
	redColor     = "\033[31m"
	greenColor   = "\033[32m"
	yellowColor  = "\033[33m"
	cyanColor    = "\033[36m"
	magentaColor = "\033[35m"
	boldColor    = "\033[1m"
)

var (
	outputMu  sync.Mutex
	jobOutput io.Writer = os.Stdout
)

func outf(format string, args ...any) {
	outputMu.Lock()
	defer outputMu.Unlock()
	fmt.Fprintf(jobOutput, format, args...)
}

const baseURL = "https://api.gamemania.co.ke/main/api"

var (
	proxyURLs    []string
	proxyIndex   uint64
	proxyClients = map[string]*http.Client{}
	proxyMu      sync.Mutex
)

func newTransport(proxy *url.URL) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		MaxIdleConns:          300,
		MaxIdleConnsPerHost:   60,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}

var directClient = &http.Client{Transport: newTransport(nil), Timeout: 20 * time.Second}

func proxyURL(line string) string {
	parts := strings.Split(strings.TrimSpace(line), ":")
	if len(parts) != 4 {
		return ""
	}
	return fmt.Sprintf("http://%s:%s@%s:%s", parts[2], parts[3], parts[0], parts[1])
}

func loadProxies() {
	env := strings.TrimSpace(os.Getenv("GAMENOMA_PROXY"))
	if strings.EqualFold(env, "none") || strings.EqualFold(env, "off") || strings.EqualFold(env, "direct") || env == "" {
		proxyURLs = nil
		return
	}
	for _, line := range strings.Split(env, ",") {
		if u := proxyURL(line); u != "" {
			proxyURLs = append(proxyURLs, u)
		}
	}
}

func nextProxy() string {
	if len(proxyURLs) == 0 {
		return ""
	}
	i := atomic.AddUint64(&proxyIndex, 1) - 1
	return proxyURLs[i%uint64(len(proxyURLs))]
}

func clientForProxy(raw string) *http.Client {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	if c, ok := proxyClients[raw]; ok {
		return c
	}
	u, err := url.Parse(raw)
	if err != nil {
		return directClient
	}
	c := &http.Client{Transport: newTransport(u), Timeout: 20 * time.Second}
	proxyClients[raw] = c
	return c
}

func doAPI(path string, payload map[string]any) (int, []byte, error) {
	body, _ := json.Marshal(payload)
	headers := map[string]string{
		"User-Agent":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
		"Accept":       "application/json",
		"Content-Type": "application/json",
		"Origin":       "https://www.gamemania.co.ke",
		"Referer":      "https://www.gamemania.co.ke/",
	}
	do := func(client *http.Client) (int, []byte, error) {
		req, err := http.NewRequest("POST", baseURL+path, bytes.NewReader(body))
		if err != nil {
			return 0, nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			return resp.StatusCode, nil, err
		}
		return resp.StatusCode, buf.Bytes(), nil
	}
	for i := 0; i < 3; i++ {
		status, raw, err := do(directClient)
		if err == nil {
			return status, raw, nil
		}
		time.Sleep(time.Duration(250*(i+1)) * time.Millisecond)
	}
	for {
		rawProxy := nextProxy()
		if rawProxy == "" {
			break
		}
		status, raw, err := do(clientForProxy(rawProxy))
		if err == nil {
			return status, raw, nil
		}
	}
	return 0, nil, fmt.Errorf("request failed")
}

// ---------- Gamemania types ----------

type gmUser struct {
	Nickname string  `json:"nickname"`
	Balance  float64 `json:"balance"`
	Coins    int     `json:"coins"`
	Mobile   string  `json:"mobile"`
	UserID   int     `json:"userId"`
}

type gmAuth struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Token string `json:"token"`
		UID   int    `json:"uid"`
	} `json:"data"`
}

type gmUserInfo struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data gmUser `json:"data"`
}

type gmBigWheel struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Coins   int `json:"coins"`
		Rid     int `json:"rid"`
		Type    int `json:"type"`
		Expired int `json:"expired"`
	} `json:"data"`
}

type gmCoupon struct {
	ID          int64  `json:"id"`
	Num         int    `json:"num"`
	TotalNumber int    `json:"total_number"`
	FaceValue   int    `json:"faceValue"`
	BetLimit    int    `json:"bet_limit"`
	ExpireTime  int64  `json:"expire_time"`
	Type        int    `json:"type"`
	Source      int    `json:"source"`
	Games       []int  `json:"games"`
	Label       string `json:"label"`
}

type gmCouponBet struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		LotteryID     []int `json:"lottery_id"`
		Balance       float64 `json:"balance"`
		GameStatus    int   `json:"game_status"`
		Win           float64 `json:"win"`
		Lines         []int `json:"lines"`
		HotSpin       int   `json:"hot_spin"`
		InnerRingID   int   `json:"inner_ring_id"`
		OuterRingID   int   `json:"outer_ring_id"`
		OuterRingTimes int  `json:"outer_ring_times"`
		UserReallyBet int   `json:"user_really_bet"`
		UserBetTimes  int   `json:"user_bet_times"`
		IsBetActivity int   `json:"is_bet_activity"`
		ScratcherTickets []int `json:"scratcher_tickets"`
		GamePool      int   `json:"game_pool"`
		GetTicketIncBet int  `json:"get_ticket_inc_bet"`
	} `json:"data"`
}

type account struct {
	Phone    string
	Password string
}

type jobResult struct {
	Phone   string
	Mode    string
	Status  string
	Balance string
	Coins   string
	Spin    string
	Message string
}

func normalizePhone(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if strings.HasPrefix(d, "254") && len(d) == 12 {
		return "0" + d[3:]
	}
	if len(d) == 9 && (d[0] == '7' || d[0] == '1') {
		return "0" + d
	}
	return d
}

func defaultPassword(phone, password string) string {
	phone = normalizePhone(phone)
	password = strings.TrimSpace(password)
	if password == "" {
		return phone
	}
	return password
}

func login(phone, password string) (int, gmAuth, []byte) {
	status, raw, err := doAPI("/user/login", map[string]any{
		"mobile":     phone,
		"password":   password,
		"type":       10,
		"channel":    "gm_wap",
		"device":     "gamemania-cli",
		"deviceType": 3,
	})
	var out gmAuth
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

func userInfo(token string) (int, gmUserInfo, []byte) {
	status, raw, err := doAPI("/user/info", map[string]any{"token": token})
	var out gmUserInfo
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

func bigWheel(token string) (int, gmBigWheel, []byte) {
	status, raw, err := doAPI("/user/big-wheel", map[string]any{"token": token})
	var out gmBigWheel
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

func couponAll(token string) (int, []gmCoupon, []byte) {
	url := "https://api.gamemania.co.ke/single-game/v1.0.6/coupon/all?token=" + url.QueryEscape(token)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := directClient.Do(req)
	if err != nil {
		return 0, nil, nil
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	var out struct {
		Code int        `json:"code"`
		Data []gmCoupon `json:"data"`
	}
	_ = json.Unmarshal(buf.Bytes(), &out)
	return resp.StatusCode, out.Data, buf.Bytes()
}

func couponBet(token string, couponID int64, dataID int, betAmount float64, roomId int) (int, gmCouponBet, []byte) {
	body := fmt.Sprintf("data={\"id\":%d,\"bet_amount\":%.2f}&coupon_id=%d&coupon_type=1&token=%s&roomId=%d&device=0",
		dataID, betAmount, couponID, url.QueryEscape(token), roomId)
	req, _ := http.NewRequest("POST", "https://api.gamemania.co.ke/single-game/v1.0.6/coupon/bet", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	resp, err := directClient.Do(req)
	if err != nil {
		return 0, gmCouponBet{}, nil
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	var out gmCouponBet
	_ = json.Unmarshal(buf.Bytes(), &out)
	return resp.StatusCode, out, buf.Bytes()
}

func hotSpinInit(token string) (int, []byte, error) {
	status, raw, err := doAPI("/single-game/v1.0.6/game/hot-spin/init", map[string]any{"token": token})
	return status, raw, err
}

func processAccount(acc account, mode string) jobResult {
	res := jobResult{Phone: acc.Phone, Mode: mode}
	phone := normalizePhone(acc.Phone)
	password := defaultPassword(phone, acc.Password)
	if phone == "" {
		res.Status = "ERROR"
		res.Message = "invalid phone"
		return res
	}
	status, auth, raw := login(phone, password)
	if status != 200 || auth.Code != 200 || auth.Data.Token == "" {
		res.Status = "ERROR"
		if auth.Msg != "" {
			res.Message = auth.Msg
		} else {
			res.Message = fmt.Sprintf("HTTP %d: %s", status, string(raw))
		}
		return res
	}
	token := auth.Data.Token
	res.Status = "OK"
	ist, info, _ := userInfo(token)
	if ist == 200 && info.Code == 200 {
		res.Balance = fmt.Sprintf("%.2f", info.Data.Balance)
		res.Coins = fmt.Sprintf("%d", info.Data.Coins)
	} else {
		res.Balance = "?"
	}
	if mode == "balance" {
		return res
	}
	// Hot Spin game flow: get coupons, pick the 2-shilling stake-5 bonus coupon
	_, coupons, _ := couponAll(token)
	if len(coupons) == 0 {
		res.Spin = "no available gift"
		return res
	}
	// pick coupon with faceValue=2 and bet_limit=5 (the bonus coupon)
	c := coupons[0]
	for _, cp := range coupons {
		if cp.FaceValue == 2 && cp.BetLimit == 5 {
			c = cp
			break
		}
	}
	// fallback: prefer "First Deposit" label
	for _, cp := range coupons {
		if strings.Contains(strings.ToLower(cp.Label), "first deposit") {
			c = cp
			break
		}
	}
	// The coupon has c.Num spins (125-250). Play all of them.
	outf("  %s: playing %d spins with coupon %d\n", phone, c.Num, c.ID)
	// play until "no available gift" or balance depleted
	totalWin := 0.0
	played := 0
	freeSpins := 0
	for {
		sst, bet, sraw := couponBet(token, c.ID, 20, 0.25, 3013)
		if sst != 200 || bet.Code != 200 {
			msg := bet.Message
			if msg == "" {
				msg = string(sraw)
			}
			if strings.Contains(strings.ToLower(msg), "no available") {
				res.Spin = "no available gift"
			} else {
				res.Spin = fmt.Sprintf("bet failed: %s", msg)
			}
			break
		}
		totalWin += bet.Data.Win
		played++
		if bet.Data.HotSpin > 0 {
			freeSpins += bet.Data.HotSpin
		}
		if len(bet.Data.ScratcherTickets) > 0 {
			freeSpins += len(bet.Data.ScratcherTickets)
		}
		if bet.Data.Balance > 0 {
			res.Balance = fmt.Sprintf("%.2f", bet.Data.Balance)
		}
		// only stop when "no available gift" message is received from the API
		if strings.Contains(strings.ToLower(bet.Message), "no available") {
			res.Spin = "no available gift"
			break
		}
	}
	// refresh final balance and coins
	_, info3, _ := userInfo(token)
	info = info3
	if info.Code == 200 {
		res.Coins = fmt.Sprintf("%d", info.Data.Coins)
		res.Balance = fmt.Sprintf("%.2f", info.Data.Balance)
	}
	if res.Spin == "no available gift" {
		// keep as is, but append free spins if any
		if freeSpins > 0 {
			res.Spin = fmt.Sprintf("no available gift (free spins: %d)", freeSpins)
		}
	} else if totalWin > 0 {
		msg := fmt.Sprintf("won %.2f (played %d spins)", totalWin, played)
		if freeSpins > 0 {
			msg += fmt.Sprintf(" + %d free spins", freeSpins)
		}
		res.Spin = msg
	} else {
		msg := fmt.Sprintf("played %d spins, no win", played)
		if freeSpins > 0 {
			msg += fmt.Sprintf(" + %d free spins", freeSpins)
		}
		res.Spin = msg
	}
	return res
}

func runBatch(accounts []account, mode string) []jobResult {
	outf("Processing %d account(s)...\n", len(accounts))
	workers := 10
	type indexed struct {
		acc account
		res jobResult
	}
	jobs := make(chan account)
	results := make(chan indexed, len(accounts))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for acc := range jobs {
				results <- indexed{acc: acc, res: processAccount(acc, mode)}
			}
		}()
	}
	go func() {
		for _, a := range accounts {
			jobs <- a
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	var out []jobResult
	done := 0
	total := len(accounts)
	for item := range results {
		done++
		r := item.res
		out = append(out, r)
		if r.Status == "OK" {
			line := fmt.Sprintf("%s | balance: %s | coins: %s", r.Phone, r.Balance, r.Coins)
			if r.Spin != "" {
				line += " | spin: " + r.Spin
			}
			outf("%s%s (%d/%d)%s\n", greenColor, line, done, total, resetColor)
		} else {
			outf("%s%s -> ERROR: %s%s\n", redColor, r.Phone, r.Message, resetColor)
		}
	}
	_ = os.MkdirAll("results", 0o755)
	name := "results/gamemania_results_" + time.Now().Format("20060102_150405") + ".csv"
	f, err := os.Create(name)
	if err == nil {
		defer f.Close()
		w := csv.NewWriter(f)
		_ = w.Write([]string{"phone", "mode", "status", "balance", "coins", "spin", "message"})
		for _, r := range out {
			_ = w.Write([]string{r.Phone, r.Mode, r.Status, r.Balance, r.Coins, r.Spin, r.Message})
		}
		w.Flush()
		outf("Saved %s\n", name)
	}
	ok := 0
	for _, r := range out {
		if r.Status == "OK" {
			ok++
		}
	}
	outf("Done. OK: %d/%d\n", ok, len(out))
	return out
}

func parseAccounts(text string) []account {
	text = strings.TrimPrefix(text, "\ufeff")
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		h = strings.ToLower(strings.TrimSpace(h))
		h = strings.NewReplacer(" ", "_", "-", "_").Replace(h)
		idx[h] = i
	}
	phoneIdx, passIdx := -1, -1
	for _, n := range []string{"phone", "msisdn", "mobile", "number", "phone_number"} {
		if i, ok := idx[n]; ok {
			phoneIdx = i
			break
		}
	}
	for _, n := range []string{"password", "pass", "pin"} {
		if i, ok := idx[n]; ok {
			passIdx = i
			break
		}
	}
	start := 0
	if phoneIdx >= 0 || passIdx >= 0 {
		start = 1
	}
	if phoneIdx < 0 {
		phoneIdx = 0
	}
	if passIdx < 0 {
		passIdx = 1
	}
	seen := map[string]bool{}
	var out []account
	for _, row := range rows[start:] {
		if len(row) == 0 {
			continue
		}
		phone := ""
		if phoneIdx < len(row) {
			phone = strings.TrimSpace(strings.Trim(row[phoneIdx], "\"'"))
		}
		password := ""
		if passIdx < len(row) {
			password = strings.TrimSpace(row[passIdx])
		}
		phone = normalizePhone(phone)
		if phone == "" {
			continue
		}
		if seen[phone] {
			continue
		}
		seen[phone] = true
		out = append(out, account{Phone: phone, Password: defaultPassword(phone, password)})
	}
	return out
}
func setJobOutput(w io.Writer) {
	outputMu.Lock()
	defer outputMu.Unlock()
	jobOutput = w
}
