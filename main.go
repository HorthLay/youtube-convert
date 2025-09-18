package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gorilla/mux"
)

type ConvertRequest struct {
	URL     string `json:"url"`
	Format  string `json:"format"`  // mp3 or mp4
	Quality string `json:"quality"` // mp4: 1080,720,320 | mp3: 128,192,320
}

type ConvertResponse struct {
	Message  string `json:"message"`
	FilePath string `json:"file_path"`
}

// ================= API PART ===================

func convertHandler(w http.ResponseWriter, r *http.Request) {
	var req ConvertRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil || (req.Format != "mp3" && req.Format != "mp4") {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Ensure downloads folder exists
	os.MkdirAll("downloads", os.ModePerm)

	fileName := fmt.Sprintf("downloads/video_%d.%s", time.Now().Unix(), req.Format)
	var cmd *exec.Cmd

	if req.Format == "mp4" {
		var quality string
		switch req.Quality {
		case "1080":
			quality = "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/mp4"
		case "720":
			quality = "bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/mp4"
		case "320":
			quality = "bestvideo[height<=320][ext=mp4]+bestaudio[ext=m4a]/mp4"
		default:
			quality = "bestvideo[ext=mp4]+bestaudio[ext=m4a]/mp4"
		}
		// Force FFmpeg to mux video+audio
		cmd = exec.Command("yt-dlp", "-f", quality, "--merge-output-format", "mp4", "-o", fileName, req.URL)

	} else if req.Format == "mp3" {
		bitrate := "128"
		if req.Quality != "" {
			bitrate = req.Quality
		}
		cmd = exec.Command("yt-dlp", "-x", "--audio-format", "mp3", "--audio-quality", bitrate, "-o", fileName, req.URL)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error: %s\n%s", err.Error(), string(out)), http.StatusInternalServerError)
		return
	}

	res := ConvertResponse{
		Message:  "Conversion successful",
		FilePath: fmt.Sprintf("/downloads/%s", filepath.Base(fileName)),
	}
	json.NewEncoder(w).Encode(res)
}

func downloadsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filename := vars["filename"]
	filePath := "downloads/" + filename

	http.ServeFile(w, r, filePath)

	// delete after serving
	go func() {
		time.Sleep(2 * time.Second)
		os.Remove(filePath)
	}()
}

// ================= TELEGRAM BOT PART ===================

var userStates = make(map[int64]string) // userID -> state
var userURLs = make(map[int64]string)   // userID -> URL
var userFormat = make(map[int64]string) // userID -> mp3/mp4

func handleTelegramBot(bot *tgbotapi.BotAPI) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		chatID := update.FromChat().ID

		if update.Message != nil {
			if update.Message.Text == "/start" {
				userStates[chatID] = "waiting_url"
				msg := tgbotapi.NewMessage(chatID, "Welcome! Send me a YouTube URL to download:")
				bot.Send(msg)
				continue
			}

			if userStates[chatID] == "waiting_url" {
				userURLs[chatID] = update.Message.Text
				userStates[chatID] = "waiting_format"

				keyboard := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("🎥 MP4 (Video)", "format_mp4"),
						tgbotapi.NewInlineKeyboardButtonData("🎵 MP3 (Audio)", "format_mp3"),
					),
				)
				msg := tgbotapi.NewMessage(chatID, "Choose format:")
				msg.ReplyMarkup = keyboard
				bot.Send(msg)
			}
		}

		if update.CallbackQuery != nil {
			data := update.CallbackQuery.Data

			switch data {
			case "format_mp4":
				userFormat[chatID] = "mp4"
				userStates[chatID] = "waiting_quality_mp4"
				keyboard := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("1080p", "mp4_1080"),
						tgbotapi.NewInlineKeyboardButtonData("720p", "mp4_720"),
						tgbotapi.NewInlineKeyboardButtonData("320p", "mp4_320"),
					),
				)
				msg := tgbotapi.NewMessage(chatID, "Choose MP4 quality:")
				msg.ReplyMarkup = keyboard
				bot.Send(msg)

			case "format_mp3":
				userFormat[chatID] = "mp3"
				userStates[chatID] = "waiting_quality_mp3"
				keyboard := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("128k", "mp3_128"),
						tgbotapi.NewInlineKeyboardButtonData("192k", "mp3_192"),
						tgbotapi.NewInlineKeyboardButtonData("320k", "mp3_320"),
					),
				)
				msg := tgbotapi.NewMessage(chatID, "Choose MP3 quality:")
				msg.ReplyMarkup = keyboard
				bot.Send(msg)

			case "mp4_1080", "mp4_720", "mp4_320", "mp3_128", "mp3_192", "mp3_320":
				var quality string
				if strings.HasPrefix(data, "mp4") {
					quality = strings.TrimPrefix(data, "mp4_")
				} else {
					quality = strings.TrimPrefix(data, "mp3_")
				}

				// tell user to wait
				waitMsg := tgbotapi.NewMessage(chatID, "⏳ Please wait, downloading and converting your file...")
				sentMsg, _ := bot.Send(waitMsg)

				req := ConvertRequest{
					URL:     userURLs[chatID],
					Format:  userFormat[chatID],
					Quality: quality,
				}
				reqBody, _ := json.Marshal(req)

				resp, err := http.Post("http://localhost:8080/api/convert", "application/json", bytes.NewBuffer(reqBody))
				if err != nil {
					bot.Send(tgbotapi.NewMessage(chatID, "API error: "+err.Error()))
					continue
				}
				defer resp.Body.Close()

				var apiResp ConvertResponse
				json.NewDecoder(resp.Body).Decode(&apiResp)

				// Download file from API
				downloadURL := "http://localhost:8080" + apiResp.FilePath
				fileResp, err := http.Get(downloadURL)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(chatID, "Download error"))
					continue
				}
				defer fileResp.Body.Close()

				tempFile, err := os.CreateTemp("", "tgfile_*."+req.Format)
				if err != nil {
					bot.Send(tgbotapi.NewMessage(chatID, "File error"))
					continue
				}
				io.Copy(tempFile, fileResp.Body)
				tempFile.Seek(0, 0)

				// send file
				if req.Format == "mp3" {
					audio := tgbotapi.NewAudio(chatID, tgbotapi.FilePath(tempFile.Name()))
					bot.Send(audio)
				} else {
					video := tgbotapi.NewVideo(chatID, tgbotapi.FilePath(tempFile.Name()))
					bot.Send(video)
				}

				// delete local temp
				tempFile.Close()
				os.Remove(tempFile.Name())

				// edit wait message to success
				edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "✅ Done! Your file has been sent.")
				bot.Send(edit)
			}
		}
	}
}

// ================= MAIN ===================

func main() {
	// Start API server
	go func() {
		r := mux.NewRouter()
		r.HandleFunc("/api/convert", convertHandler).Methods("POST")
		r.HandleFunc("/downloads/{filename}", downloadsHandler).Methods("GET")

		fmt.Println("API running at http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", r))
	}()

	// Start Telegram bot
	bot, err := tgbotapi.NewBotAPI("7144449198:AAHoMTB8azWfEo1WvbQ4A_EBF-2lO-TsNxQ")
	if err != nil {
		log.Fatal("Telegram bot error:", err)
	}
	fmt.Println("Telegram bot started as", bot.Self.UserName)

	handleTelegramBot(bot)
}
