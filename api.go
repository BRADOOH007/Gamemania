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
	"strconv"
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
	Nickname      string  `json:"nickname"`
	Balance       float64 `json:"balance"`
	Coins         int     `json:"coins"`
	Mobile        string  `json:"mobile"`
	UserID        int     `json:"userId"`
	DaysNumber    int     `json:"daysNumber"`
	IsAttendance  bool    `json:"isAttendance"`
	SingleGifts   int     `json:"singleGifts"`
	GiftsNumber   int     `json:"giftsNumber"`
	FirstDeposit  int     `json:"first_deposit_activity"`
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
		UserReallyBet int        `json:"user_really_bet"`
		UserBetTimes  int        `json:"user_bet_times"`
		IsBetActivity int        `json:"is_bet_activity"`
		ScratcherTickets []gmTicket `json:"scratcher_tickets"`
		GamePool      int        `json:"game_pool"`
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

// gmSignIn is the daily check-in ("Check in Now") that feeds the coin balance.
type gmSignIn struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Coins        int  `json:"coins"`
		DaysNumber   int  `json:"daysNumber"`
		IsAttendance bool `json:"isAttendance"`
	} `json:"data"`
}

func signIn(token string) (int, gmSignIn, []byte) {
	status, raw, err := doAPI("/user/sign-in", map[string]any{"token": token})
	var out gmSignIn
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

// ---------- Hot Spin game ----------
//
// Mirrors the real game client (casino2-release / hotSpin):
//   init   POST /game/hot-spin/init   token, game_id=3013, is_gift=0, roomId=3013, device=0
//   bet    POST /coupon/bet           token, data={"id":20,"bet_amount":0.25}, coupon_id, coupon_type, roomId, device
//   bet    POST /game/hot-spin/bet    same, no coupon fields (used only when no gift is left)
//   free   POST /game/hot-spin/lucky  token, roomId, device
//   gift   POST /game/scratcher/handle token, game_id, id
//
// Stake = 20 lines * 0.25 = 5 KES. A type-1 gift covers `faceValue` KES of the
// stake (2 KES here), so a spin really costs 3 KES while the gift lasts.

const (
	gameBaseURL   = "https://api.gamemania.co.ke/single-game/v1.0.6"
	hotSpinGameID = 3013
	hotSpinLines  = 20
	hotSpinBPL    = 0.25
	hotSpinStake  = hotSpinLines * hotSpinBPL
)

type gmTicket struct {
	ID int `json:"id"`
}

type gmHotSpin struct {
	HasHotSpin      int    `json:"has_hot_spin"`
	PlayTimes       int    `json:"play_times"`
	OuterRingID     int    `json:"outer_ring_id"`
	InnerRingID     int    `json:"inner_ring_id"`
	OuterRingTimes  int    `json:"outer_ring_times"`
	FreeSpinData    any    `json:"free_spin_data"`
}

type gmHotSpinInit struct {
	Code int `json:"code"`
	Data struct {
		GameStatus       int        `json:"game_status"`
		HotSpin          gmHotSpin  `json:"hot_spin"`
		UserReallyBet    float64    `json:"user_really_bet"`
		UserBetTimes     int        `json:"user_bet_times"`
		ScratcherTickets []gmTicket `json:"scratcher_tickets"`
	} `json:"data"`
	Message string `json:"message"`
}

func gamePost(path, body string) (int, []byte, error) {
	req, err := http.NewRequest("POST", gameBaseURL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://www.gamemania.co.ke")
	req.Header.Set("Referer", "https://www.gamemania.co.ke/")
	resp, err := directClient.Do(req)
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

func hotSpinInit(token string) (int, gmHotSpinInit, []byte) {
	body := fmt.Sprintf("token=%s&game_id=%d&is_gift=0&roomId=%d&device=0",
		url.QueryEscape(token), hotSpinGameID, hotSpinGameID)
	status, raw, err := gamePost("/game/hot-spin/init", body)
	var out gmHotSpinInit
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

func luckySpin(token string) (int, gmCouponBet, []byte) {
	body := fmt.Sprintf("token=%s&roomId=%d&device=0", url.QueryEscape(token), hotSpinGameID)
	status, raw, err := gamePost("/game/hot-spin/lucky", body)
	var out gmCouponBet
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

func claimTicket(token string, id int) (int, gmCouponBet, []byte) {
	body := fmt.Sprintf("token=%s&game_id=%d&id=%d", url.QueryEscape(token), hotSpinGameID, id)
	status, raw, err := gamePost("/game/scratcher/handle", body)
	var out gmCouponBet
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

// placeBet sends one spin. `coupon` may be nil (plain game bet, no gift).
func placeBet(token string, coupon *gmCoupon) (int, gmCouponBet, []byte) {
	data := fmt.Sprintf("data={\"id\":%d,\"bet_amount\":%.2f}", hotSpinLines, hotSpinBPL)
	var path, body string
	if coupon != nil {
		path = "/coupon/bet"
		body = data + fmt.Sprintf("&coupon_id=%d&coupon_type=%d&token=%s&roomId=%d&device=0",
			coupon.ID, coupon.Type, url.QueryEscape(token), hotSpinGameID)
	} else {
		path = "/game/hot-spin/bet"
		body = data + fmt.Sprintf("&token=%s&roomId=%d&device=0",
			url.QueryEscape(token), hotSpinGameID)
	}
	status, raw, err := gamePost(path, body)
	var out gmCouponBet
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return status, out, raw
}

func logout(token string) {
	if token == "" {
		return
	}
	_, _, _ = doAPI("/logout", map[string]any{"token": token})
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// pickCoupon mirrors the client's getCanUseCoupon(): type-1 gifts only apply when
// bet_limit <= stake; type-2 gifts apply to any stake. Highest faceValue wins.
func pickCoupon(coupons []gmCoupon, stake float64) *gmCoupon {
	var best *gmCoupon
	for i := range coupons {
		c := &coupons[i]
		if c.Num <= 0 {
			continue
		}
		if len(c.Games) > 0 && !containsInt(c.Games, hotSpinGameID) {
			continue
		}
		if c.Type == 1 {
			if float64(c.BetLimit) > stake {
				continue
			}
		} else if c.Type != 2 {
			continue
		}
		if best == nil || c.FaceValue > best.FaceValue {
			best = c
		}
	}
	return best
}

func spinDelay() time.Duration {
	ms := 1500 // let each spin animation settle before the next request
	if v := strings.TrimSpace(os.Getenv("GAMEMANIA_SPIN_DELAY_MS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			ms = n
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// playHotSpin runs the "UNTIL STOP" auto spin: keep spinning with the active
// gift until the API has no usable gift left ("no available gifts"), then stop.
func playHotSpin(token, phone string) (played int, totalWin float64, freeSpins, tickets int, gameBal float64, haveBal bool, reason string) {
	delay := spinDelay()

	// Finish any free spins left over from a previous session first.
	if _, init, _ := hotSpinInit(token); init.Code == 200 && init.Data.GameStatus == 1 && init.Data.HotSpin.HasHotSpin > 0 {
		pending := init.Data.HotSpin.OuterRingTimes - init.Data.HotSpin.PlayTimes
		if pending < 0 {
			pending = 0
		}
		if pending > 0 {
			outf("%s%s  %s: >>> FREE SPINS pending from last run: %d - playing them now%s\n",
				boldColor, cyanColor, phone, pending, resetColor)
		}
		pendingWin := 0.0
		for i := 0; i < pending; i++ {
			code, lres, _ := luckySpin(token)
			if code != 200 || lres.Code != 200 {
				break
			}
			freeSpins++
			pendingWin += lres.Data.Win
			totalWin += lres.Data.Win
			gameBal = lres.Data.Balance
			haveBal = true
			time.Sleep(delay / 2)
		}
		if pending > 0 {
			outf("%s  %s: pending free spins done, won %.2f | balance %.2f%s\n",
				cyanColor, phone, pendingWin, gameBal, resetColor)
		}
	}

	_, coupons, _ := couponAll(token)
	c := pickCoupon(coupons, hotSpinStake)
	if c == nil {
		return 0, totalWin, freeSpins, 0, gameBal, haveBal, "no available gift"
	}
	outf("  %s: gift %d loaded with %d spins (stake %.2f, pays %.2f/spin)\n",
		phone, c.ID, c.Num, hotSpinStake, hotSpinStake-float64(c.FaceValue))
	invalid := 0
	for played < 5000 {
		if c == nil || c.Num <= 0 {
			// This gift is spent - look for another one before giving up.
			spentID := int64(0)
			if c != nil {
				spentID = c.ID
			}
			_, cps, _ := couponAll(token)
			for i := range cps {
				if cps[i].ID == spentID {
					cps[i].Num = 0
				}
			}
			if c = pickCoupon(cps, hotSpinStake); c == nil {
				reason = "no available gift"
				break
			}
		}
		cost := hotSpinStake - float64(c.FaceValue)
		if cost < 0 {
			cost = 0
		}
		if haveBal && gameBal < cost {
			reason = fmt.Sprintf("balance too low (%.2f < %.2f)", gameBal, cost)
			break
		}
		status, bet, raw := placeBet(token, c)
		if status != 200 || bet.Code != 200 {
			msg := bet.Message
			if msg == "" {
				msg = strings.TrimSpace(string(raw))
			}
			low := strings.ToLower(msg)
			switch {
			case strings.Contains(low, "no available"):
				reason = "no available gift"
			case bet.Code == 407 || strings.Contains(low, "coupon invalid"):
				// Gift expired or spent server-side - look for another one.
				invalid++
				if invalid > 3 {
					reason = "no available gift (" + msg + ")"
					break
				}
				_, cps, _ := couponAll(token)
				if c = pickCoupon(cps, hotSpinStake); c != nil {
					continue
				}
				reason = "no available gift"
			case bet.Code == 300:
				reason = "session expired"
			case strings.Contains(low, "balance") || bet.Code == 5002:
				reason = "balance too low: " + msg
			default:
				reason = "bet failed: " + msg
			}
			break
		}
		played++
		totalWin += bet.Data.Win
		gameBal = bet.Data.Balance
		haveBal = true
		c.Num--

		// Bonus tickets ("free bets") dropped by this spin. The client claims
		// each one through /game/scratcher/handle; the win lands in the wallet.
		for _, t := range bet.Data.ScratcherTickets {
			if t.ID == 0 {
				continue
			}
			tickets++
			sc, sres, _ := claimTicket(token, t.ID)
			if sc == 200 && sres.Code == 200 {
				totalWin += sres.Data.Win
				gameBal = sres.Data.Balance
				haveBal = true
				outf("%s%s  %s: >>> FREE BET #%d awarded (id %d) -> won %.2f | balance %.2f%s\n",
					boldColor, magentaColor, phone, tickets, t.ID, sres.Data.Win, gameBal, resetColor)
			} else {
				outf("%s  %s: >>> FREE BET #%d dropped (id %d) but claim failed: %s%s\n",
					yellowColor, phone, tickets, t.ID, sres.Message, resetColor)
			}
		}

		// Hot-spin wheel / free spins awarded by this spin.
		// The wheel (outer_ring_id) is only animation: the server already decided
		// how many free spins you get in `outer_ring_times` (3, 4, ...). The client
		// then plays that many free spins through /game/hot-spin/lucky.
		fs := 0
		if bet.Data.HotSpin > 0 && bet.Data.OuterRingTimes > 0 {
			fs = bet.Data.OuterRingTimes
			outf("%s%s  %s: >>> FREE SPINS AWARDED: %d spins (wheel %d) - playing them now%s\n",
				boldColor, cyanColor, phone, fs, bet.Data.OuterRingID, resetColor)
		}
		fsDone := 0
		fsWin := 0.0
		for i := 0; i < fs; i++ {
			lc, lres, _ := luckySpin(token)
			if lc != 200 || lres.Code != 200 {
				break
			}
			fsDone++
			freeSpins++
			fsWin += lres.Data.Win
			totalWin += lres.Data.Win
			gameBal = lres.Data.Balance
			haveBal = true
			time.Sleep(delay / 2)
		}
		if fs > 0 {
			outf("%s  %s: free spins done %d/%d, won %.2f | balance %.2f%s\n",
				cyanColor, phone, fsDone, fs, fsWin, gameBal, resetColor)
		}

		// Re-sync the gift every 20 spins: only check that it is still offered.
		// (The real client counts uses locally, so we must not reset our count.)
		if played%20 == 0 {
			_, cps, _ := couponAll(token)
			stillThere := false
			for _, cp := range cps {
				if cp.ID == c.ID && cp.Num > 0 {
					stillThere = true
					break
				}
			}
			if !stillThere {
				if c = pickCoupon(cps, hotSpinStake); c == nil {
					reason = "no available gift"
					break
				}
			}
			outf("  %s: %d spins done, win %.2f, free %d, tickets %d, gift %d has %d left, balance %.2f\n",
				phone, played, totalWin, freeSpins, tickets, c.ID, c.Num, gameBal)
		}
		time.Sleep(delay)
	}
	if reason == "" {
		reason = "stopped"
	}
	return
}

// ---------- Daily coins ----------
//
// Coins are earned by the daily check-in and by playing; the only way to spend
// them is the Coins Spin (POST /user/big-wheel), 20 coins per draw. Prizes go
// straight to the account: cash to the main wallet, gifts to My Benefits ->
// Gifts, energy to Africa Zoo.

const coinSpinCost = 20

// coinPrize maps the server `rid` to the wheel slice label. The client indexes
// its slice table with rid and skips the result popup for rid 3 ("Try Again").
var coinPrize = map[int]string{
	1:  "Ksh 5 Cash",
	2:  "Ksh 10 Cash",
	3:  "Try Again",
	4:  "Ksh 1 Cash",
	5:  "Ksh 2 Cash",
	6:  "Ksh 5 Gift",
	7:  "Ksh 2 Gift x5",
	8:  "Ksh 100 Cash",
	9:  "Ksh 500 Cash",
	10: "Ksh 1000 Cash",
	11: "10 Energy",
	12: "20 Energy",
}

func playCoins(token, phone string) (checkedIn bool, checkInGain int, spins int, prizes []string, coinsStart, coinsEnd int, reason string) {
	delay := spinDelay() / 2
	if delay < 300*time.Millisecond {
		delay = 300 * time.Millisecond
	}

	ist, info, _ := userInfo(token)
	if ist != 200 || info.Code != 200 {
		return false, 0, 0, nil, 0, 0, "cannot read coin balance"
	}
	coinsStart, coinsEnd = info.Data.Coins, info.Data.Coins
	outf("  %s: %d coins, streak day %d, checked in today: %t\n",
		phone, coinsStart, info.Data.DaysNumber, info.Data.IsAttendance)

	// 1. Daily check-in.
	if !info.Data.IsAttendance {
		sc, sres, _ := signIn(token)
		if sc == 200 && sres.Code == 200 {
			checkedIn = true
			before := coinsEnd
			if sres.Data.Coins > 0 {
				coinsEnd = sres.Data.Coins
			}
			checkInGain = coinsEnd - before
			if checkInGain <= 0 {
				checkInGain = coinCheckInReward(info.Data.DaysNumber)
				coinsEnd = before + checkInGain
			}
			outf("%s  %s: >>> CHECK-IN +%d coins (streak day %d) -> %d coins%s\n",
				greenColor, phone, checkInGain, sres.Data.DaysNumber, coinsEnd, resetColor)
		} else {
			outf("%s  %s: check-in failed: %s%s\n", yellowColor, phone, signInMsg(sres.Msg, sc), resetColor)
		}
	} else {
		outf("  %s: already checked in today - no daily coins\n", phone)
	}

	// 2. Spend coins on the wheel, 20 per draw.
	for spins < 50 && coinsEnd >= coinSpinCost {
		status, res, raw := bigWheel(token)
		if status != 200 || res.Code != 200 {
			msg := res.Msg
			if msg == "" {
				msg = strings.TrimSpace(string(raw))
			}
			reason = "coin spin failed: " + msg
			break
		}
		spins++
		label := coinPrize[res.Data.Rid]
		if label == "" {
			label = fmt.Sprintf("reward #%d", res.Data.Rid)
		}
		if res.Data.Rid != 3 {
			prizes = append(prizes, label)
		}
		coinsEnd = res.Data.Coins
		outf("%s  %s: >>> COIN SPIN %d -> %s | %d coins left%s\n",
			magentaColor, phone, spins, label, coinsEnd, resetColor)
		time.Sleep(delay)
	}

	if reason == "" {
		switch {
		case spins > 0:
			reason = "coins spent"
		case coinsEnd < coinSpinCost:
			reason = fmt.Sprintf("not enough coins (%d < %d)", coinsEnd, coinSpinCost)
		default:
			reason = "stopped"
		}
	}
	return
}

// coinCheckInReward is the published 7-day check-in ladder (+20/+20/+40/+45/
// +60/+65/+100) used only when the server response omits the new balance.
func coinCheckInReward(streakDay int) int {
	ladder := []int{20, 20, 40, 45, 60, 65, 100}
	if streakDay < 0 || streakDay >= len(ladder) {
		return 20
	}
	return ladder[streakDay]
}

func signInMsg(msg string, status int) string {
	if msg != "" {
		return msg
	}
	return fmt.Sprintf("HTTP %d", status)
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
	defer logout(token)
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

	if mode == "coins" {
		coinsBefore := info.Data.Coins
		checkedIn, gain, spins, prizes, _, coinsAfter, reason := playCoins(token, phone)
		if _, info2, _ := userInfo(token); info2.Code == 200 {
			res.Balance = fmt.Sprintf("%.2f", info2.Data.Balance)
			res.Coins = fmt.Sprintf("%d", info2.Data.Coins)
		} else {
			res.Coins = fmt.Sprintf("%d", coinsAfter)
		}
		parts := []string{reason}
		if checkedIn {
			parts = append(parts, fmt.Sprintf("check-in +%d", gain))
		} else {
			parts = append(parts, "check-in skipped")
		}
		parts = append(parts, fmt.Sprintf("spins %d", spins))
		if len(prizes) > 0 {
			parts = append(parts, "won "+strings.Join(prizes, ", "))
		}
		parts = append(parts, fmt.Sprintf("coins %d->%d", coinsBefore, coinsAfter))
		res.Spin = strings.Join(parts, " | ")
		return res
	}

	startBal := info.Data.Balance
	played, totalWin, freeSpins, tickets, _, _, reason := playHotSpin(token, phone)

	// Refresh the authoritative wallet after the run.
	if _, info2, _ := userInfo(token); info2.Code == 200 {
		res.Balance = fmt.Sprintf("%.2f", info2.Data.Balance)
		res.Coins = fmt.Sprintf("%d", info2.Data.Coins)
		endBal := info2.Data.Balance
		parts := []string{reason}
		parts = append(parts, fmt.Sprintf("played %d", played))
		parts = append(parts, fmt.Sprintf("win %.2f", totalWin))
		if freeSpins > 0 {
			parts = append(parts, fmt.Sprintf("free spins %d", freeSpins))
		}
		if tickets > 0 {
			parts = append(parts, fmt.Sprintf("free bets %d", tickets))
		}
		parts = append(parts, fmt.Sprintf("bal %.2f->%.2f", startBal, endBal))
		res.Spin = strings.Join(parts, " | ")
	} else {
		res.Spin = fmt.Sprintf("%s | played %d | win %.2f", reason, played, totalWin)
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
