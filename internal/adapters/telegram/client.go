package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"expensemother/internal/shared"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	Token, BaseURL string
	HTTP           *http.Client
}

func NewClient(token string) *Client {
	return &Client{Token: token, BaseURL: "https://api.telegram.org", HTTP: &http.Client{Timeout: 55 * time.Second}}
}
func (c *Client) Call(ctx context.Context, method string, input, out any) error {
	b, e := json.Marshal(input)
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/bot"+c.Token+"/"+method, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return fmt.Errorf("Telegram %s unavailable", method)
	}
	defer resp.Body.Close()
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Code   int             `json:"error_code"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope) != nil || !envelope.OK {
		return fmt.Errorf("Telegram %s failed (status %d)", method, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
func (c *Client) Member(ctx context.Context, chat, user int64) (bool, bool, error) {
	var m struct {
		Status   string
		IsMember bool `json:"is_member"`
	}
	if e := c.Call(ctx, "getChatMember", map[string]any{"chat_id": chat, "user_id": user}, &m); e != nil {
		return false, false, e
	}
	admin := m.Status == "creator" || m.Status == "administrator"
	return admin || m.Status == "member" || m.Status == "restricted" && m.IsMember, admin, nil
}
func (c *Client) Download(ctx context.Context, file string) ([]byte, error) {
	var info struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if e := c.Call(ctx, "getFile", map[string]any{"file_id": file}, &info); e != nil {
		return nil, e
	}
	if info.Size > 10<<20 || info.Path == "" || strings.Contains(info.Path, "..") {
		return nil, shared.Bad("receipt exceeds supported size")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/file/bot"+c.Token+"/"+info.Path, nil)
	if e != nil {
		return nil, e
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return nil, fmt.Errorf("Telegram image download unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Telegram image download failed")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
	if len(b) > 10<<20 {
		return nil, shared.Bad("maximum receipt size 10 MiB")
	}
	return b, e
}

type User struct {
	ID       int64  `json:"id"`
	First    string `json:"first_name"`
	Last     string `json:"last_name"`
	Language string `json:"language_code"`
	IsBot    bool   `json:"is_bot"`
}
type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}
type Photo struct {
	ID   string `json:"file_id"`
	Size int    `json:"file_size"`
}
type Message struct {
	ID       int64    `json:"message_id"`
	From     User     `json:"from"`
	Chat     Chat     `json:"chat"`
	Text     string   `json:"text"`
	Reply    *Message `json:"reply_to_message"`
	Photos   []Photo  `json:"photo"`
	Document *Photo   `json:"document"`
}
type Callback struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}
type Update struct {
	ID       int64     `json:"update_id"`
	Message  *Message  `json:"message"`
	Callback *Callback `json:"callback_query"`
}
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data,omitempty"`
	URL  string `json:"url,omitempty"`
}
type Keyboard struct {
	Rows [][]Button `json:"inline_keyboard"`
}

func (c *Client) Send(ctx context.Context, chat int64, text string, rows [][]Button) (int64, error) {
	var m Message
	input := map[string]any{"chat_id": chat, "text": text}
	if rows != nil {
		input["reply_markup"] = Keyboard{rows}
	}
	err := c.Call(ctx, "sendMessage", input, &m)
	return m.ID, err
}

func (c *Client) SendReceipt(ctx context.Context, chat int64, image io.Reader, contentType string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", strconv.FormatInt(chat, 10))
	extension := map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/webp": "webp"}[contentType]
	if extension == "" {
		extension = "bin"
	}
	part, e := writer.CreateFormFile("document", "receipt."+extension)
	if e != nil {
		return e
	}
	if _, e = io.Copy(part, io.LimitReader(image, (10<<20)+1)); e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/bot"+c.Token+"/sendDocument", &body)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return fmt.Errorf("receipt delivery unavailable")
	}
	defer resp.Body.Close()
	var result struct{ OK bool }
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || !result.OK {
		return fmt.Errorf("receipt delivery failed")
	}
	return nil
}
