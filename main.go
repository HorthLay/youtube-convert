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

// ================= STRUCTS ===================

type ConvertRequest struct {
	URL     string `json:"url"`
	Format  string `json:"format"`  // mp3, mp4, image
	Quality string `json:"quality"` // mp4: 1080,720,320 | mp3: 128,192,320
}

type ConvertResponse struct {
	Message  string `json:"message"`
	FilePath string `json:"file_path"`
	Error    string `json:"error,omitempty"`
}

// ================= API HANDLERS ===================

func convertHandler(w http.ResponseWriter, r *http.Request) {
	var req ConvertRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// Ensure downloads folder exists
	os.MkdirAll("downloads", os.ModePerm)

	var fileName string
	var cmd *exec.Cmd

	switch req.Format {
	case "mp4":
		fileName = fmt.Sprintf("downloads/video_%d.mp4", time.Now().Unix())
		quality := "best[ext=mp4]/bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best"
		if req.Quality != "" {
			quality = fmt.Sprintf(
				"bestvideo[height<=%s][ext=mp4]+bestaudio[ext=m4a]/bestvideo[height<=%s]+bestaudio/best[height<=%s]",
				req.Quality, req.Quality, req.Quality,
			)
		}

		cmd = exec.Command("yt-dlp",
			"-f", quality,
			"--merge-output-format", "mp4",
			"--no-playlist",
			"--no-abort-on-error",
			"--continue",
			"--no-overwrites",
			"--restrict-filenames",
			"-o", fileName,
			req.URL)

	case "mp3":
		fileName = fmt.Sprintf("downloads/audio_%d.%%(ext)s", time.Now().Unix())
		bitrate := "128"
		if req.Quality != "" {
			bitrate = req.Quality
		}

		cmd = exec.Command("yt-dlp",
			"-x",
			"--audio-format", "mp3",
			"--audio-quality", bitrate,
			"--no-playlist",
			"--no-abort-on-error",
			"--continue",
			"--no-overwrites",
			"--restrict-filenames",
			"-o", fileName,
			req.URL)

	case "image":
		fileName = fmt.Sprintf("downloads/image_%d.%%(ext)s", time.Now().Unix())
		cmd = exec.Command("yt-dlp",
			"--skip-download",
			"--write-thumbnail",
			"--no-playlist",
			"--convert-thumbnails", "jpg",
			"--restrict-filenames",
			"-o", fileName,
			req.URL)

	default:
		res := ConvertResponse{Error: "Invalid format"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(res)
		return
	}

	log.Printf("Executing command: %v", cmd.Args)
	out, err := cmd.CombinedOutput()

	if err != nil {
		log.Printf("Command error: %s\nOutput: %s", err.Error(), string(out))
		res := ConvertResponse{
			Error: fmt.Sprintf("Conversion failed: %s", err.Error()),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(res)
		return
	}

	// Handle dynamic file extension for audio and images
	if strings.Contains(fileName, "%(ext)s") {
		matches, _ := filepath.Glob(strings.Replace(fileName, "%(ext)s", "*", 1))
		if len(matches) > 0 {
			fileName = matches[0]
		} else {
			res := ConvertResponse{Error: "Converted file not found"}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(res)
			return
		}
	}

	// Verify file exists and has content
	if stat, err := os.Stat(fileName); err != nil || stat.Size() == 0 {
		res := ConvertResponse{Error: "Converted file is empty or not found"}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(res)
		return
	}

	res := ConvertResponse{
		Message:  "Conversion successful",
		FilePath: fmt.Sprintf("/downloads/%s", filepath.Base(fileName)),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

func downloadsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filename := vars["filename"]
	filePath := "downloads/" + filename

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, filePath)

	go func() {
		time.Sleep(10 * time.Second)
		if err := os.Remove(filePath); err != nil {
			log.Printf("Error deleting file %s: %v", filePath, err)
		} else {
			log.Printf("Successfully deleted file: %s", filePath)
		}
	}()
}

// ================= TELEGRAM BOT PART ===================

var userStates = make(map[int64]string)
var userURLs = make(map[int64]string)
var userFormat = make(map[int64]string)

func handleTelegramBot(bot *tgbotapi.BotAPI) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.FromChat() == nil {
			continue
		}

		chatID := update.FromChat().ID

		if update.Message != nil {
			if update.Message.Text == "/start" {
				userStates[chatID] = "waiting_url"
				msg := tgbotapi.NewMessage(chatID, "Welcome! Send me a YouTube/Instagram/TikTok URL to download:")
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
						tgbotapi.NewInlineKeyboardButtonData("📷 Image", "format_image"),
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

			case "format_image":
				userFormat[chatID] = "image"
				processConversion(bot, chatID, "image", "")

			case "mp4_1080", "mp4_720", "mp4_320":
				quality := strings.TrimPrefix(data, "mp4_")
				processConversion(bot, chatID, "mp4", quality)

			case "mp3_128", "mp3_192", "mp3_320":
				quality := strings.TrimPrefix(data, "mp3_")
				processConversion(bot, chatID, "mp3", quality)
			}
		}
	}
}

func processConversion(bot *tgbotapi.BotAPI, chatID int64, format, quality string) {
	waitMsg := tgbotapi.NewMessage(chatID, "⏳ Please wait, downloading and converting your file... This may take several minutes for long videos.")
	sentMsg, _ := bot.Send(waitMsg)

	req := ConvertRequest{
		URL:     userURLs[chatID],
		Format:  format,
		Quality: quality,
	}
	reqBody, _ := json.Marshal(req)

	client := &http.Client{
		Timeout: 30 * time.Minute,
	}

	resp, err := client.Post("http://localhost:8080/api/convert", "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "❌ API error: "+err.Error())
		bot.Send(edit)
		return
	}
	defer resp.Body.Close()

	var apiResp ConvertResponse
	json.NewDecoder(resp.Body).Decode(&apiResp)

	if apiResp.Error != "" {
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "❌ Conversion error: "+apiResp.Error)
		bot.Send(edit)
		return
	}

	edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "📤 File converted successfully! Now uploading to Telegram...")
	bot.Send(edit)

	downloadURL := "http://localhost:8080" + apiResp.FilePath
	fileResp, err := client.Get(downloadURL)
	if err != nil {
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "❌ Download error: "+err.Error())
		bot.Send(edit)
		return
	}
	defer fileResp.Body.Close()

	tempFile, err := os.CreateTemp("", "tgfile_*."+format)
	if err != nil {
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "❌ File error: "+err.Error())
		bot.Send(edit)
		return
	}

	_, err = io.Copy(tempFile, fileResp.Body)
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "❌ File copy error: "+err.Error())
		bot.Send(edit)
		return
	}
	tempFile.Close()

	var sendErr error
	switch format {
	case "image":
		photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(tempFile.Name()))
		_, sendErr = bot.Send(photo)
	case "mp3":
		audio := tgbotapi.NewAudio(chatID, tgbotapi.FilePath(tempFile.Name()))
		_, sendErr = bot.Send(audio)
	case "mp4":
		video := tgbotapi.NewVideo(chatID, tgbotapi.FilePath(tempFile.Name()))
		_, sendErr = bot.Send(video)
	}

	os.Remove(tempFile.Name())

	if sendErr != nil {
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "❌ Failed to send file: "+sendErr.Error())
		bot.Send(edit)
		return
	}

	edit = tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, "✅ Done! Your file has been sent and temporary files have been cleaned up.")
	bot.Send(edit)

	delete(userStates, chatID)
	delete(userURLs, chatID)
	delete(userFormat, chatID)
}

// ================= MAIN ===================

func main() {
	go func() {
		r := mux.NewRouter()
		r.HandleFunc("/api/convert", convertHandler).Methods("POST")
		r.HandleFunc("/downloads/{filename}", downloadsHandler).Methods("GET")

		fmt.Println("API running at http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", r))
	}()

	bot, err := tgbotapi.NewBotAPI("7144449198:AAHoMTB8azWfEo1WvbQ4A_EBF-2lO-TsNxQ")
	if err != nil {
		log.Fatal("Telegram bot error:", err)
	}
	fmt.Println("Telegram bot started as", bot.Self.UserName)

	handleTelegramBot(bot)
}
