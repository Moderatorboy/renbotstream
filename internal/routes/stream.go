package routes

import (
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/utils"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gotd/td/tg"
	range_parser "github.com/quantumsheep/range-parser"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

var log *zap.Logger

func (e *allRoutes) LoadHome(r *Route) {
	log = e.log.Named("Stream")
	defer log.Info("Loaded stream route")
	r.Engine.GET("/stream/:channelID/:messageID", getStreamRoute)
	r.Engine.GET("/stream/:channelID/:messageID/duration", getDurationRoute)
}

// ─── DURATION ROUTE ───────────────────────────────────
// GET /stream/:channelID/:messageID/duration
// Returns: { "duration": 273 }  (seconds mein)
func getDurationRoute(ctx *gin.Context) {
	channelIDParm := ctx.Param("channelID")
	channelID, err := strconv.ParseInt(channelIDParm, 10, 64)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Channel ID"})
		return
	}

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Message ID"})
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, channelID, messageID)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if file.FileSize == 0 {
		ctx.JSON(http.StatusOK, gin.H{"duration": nil})
		return
	}

	// Last 512KB fetch karo — moov box MP4 ke end mein hota hai
	chunkSize := int64(512 * 1024)
	offset := file.FileSize - chunkSize
	if offset < 0 {
		offset = 0
		chunkSize = file.FileSize
	}

	res, err := worker.Client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
		Location: file.Location,
		Offset:   int(offset),
		Limit:    int(chunkSize),
	})
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch file chunk"})
		return
	}

	result, ok := res.(*tg.UploadFile)
	if !ok {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "Unexpected response"})
		return
	}

	// MP4 header se duration parse karo
	fileBytes := result.GetBytes()
	duration := parseMp4Duration(fileBytes)

	if duration > 0 {
		ctx.JSON(http.StatusOK, gin.H{"duration": duration})
		return
	}

	// Fallback: file size se rough estimate (avg 1.5 Mbps)
	avgBytesPerSec := int64(1500 * 1024 / 8)
	estimated := file.FileSize / avgBytesPerSec
	if estimated > 0 {
		ctx.JSON(http.StatusOK, gin.H{"duration": estimated, "estimated": true})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"duration": nil})
}

// ── MP4 mvhd box parser — exact duration nikalta hai ──
func parseMp4Duration(data []byte) int64 {
	i := 0
	for i+8 <= len(data) {
		size := int(data[i])<<24 | int(data[i+1])<<16 | int(data[i+2])<<8 | int(data[i+3])
		boxType := string(data[i+4 : i+8])

		if size < 8 {
			break
		}
		if i+size > len(data) {
			if boxType == "moov" {
				inner := parseMp4Duration(data[i+8:])
				if inner > 0 {
					return inner
				}
			}
			break
		}

		switch boxType {
		case "moov":
			inner := parseMp4Duration(data[i+8 : i+size])
			if inner > 0 {
				return inner
			}
		case "mvhd":
			offset := i + 8
			if offset >= len(data) {
				break
			}
			version := data[offset]
			offset += 4 // version(1) + flags(3)

			if version == 1 {
				offset += 16 // creation(8) + modification(8)
				if offset+12 > len(data) {
					break
				}
				timescale := int64(data[offset])<<24 | int64(data[offset+1])<<16 |
					int64(data[offset+2])<<8 | int64(data[offset+3])
				offset += 4
				duration := int64(data[offset])<<56 | int64(data[offset+1])<<48 |
					int64(data[offset+2])<<40 | int64(data[offset+3])<<32 |
					int64(data[offset+4])<<24 | int64(data[offset+5])<<16 |
					int64(data[offset+6])<<8 | int64(data[offset+7])
				if timescale > 0 {
					return duration / timescale
				}
			} else {
				offset += 8 // creation(4) + modification(4)
				if offset+8 > len(data) {
					break
				}
				timescale := int64(data[offset])<<24 | int64(data[offset+1])<<16 |
					int64(data[offset+2])<<8 | int64(data[offset+3])
				offset += 4
				duration := int64(data[offset])<<24 | int64(data[offset+1])<<16 |
					int64(data[offset+2])<<8 | int64(data[offset+3])
				if timescale > 0 {
					return duration / timescale
				}
			}
		}

		i += size
	}
	return 0
}

// ─── STREAM ROUTE ─────────────────────────────────────
func getStreamRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request
	channelIDParm := ctx.Param("channelID")
	channelID, err := strconv.ParseInt(channelIDParm, 10, 64)
	if err != nil {
		http.Error(w, "Invalid Channel ID", http.StatusBadRequest)
		return
	}
	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		http.Error(w, "Invalid Message ID", http.StatusBadRequest)
		return
	}
	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, channelID, messageID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if file.FileSize == 0 {
		res, err := worker.Client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: file.Location,
			Offset:   0,
			Limit:    1024 * 1024,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		result, ok := res.(*tg.UploadFile)
		if !ok {
			http.Error(w, "unexpected response", http.StatusInternalServerError)
			return
		}
		fileBytes := result.GetBytes()
		ctx.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", file.FileName))
		if r.Method != "HEAD" {
			ctx.Data(http.StatusOK, file.MimeType, fileBytes)
		}
		return
	}
	ctx.Header("Accept-Ranges", "bytes")
	var start, end int64
	rangeHeader := r.Header.Get("Range")
	if rangeHeader == "" {
		start = 0
		end = file.FileSize - 1
		w.WriteHeader(http.StatusOK)
	} else {
		ranges, err := range_parser.Parse(file.FileSize, r.Header.Get("Range"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		start = ranges[0].Start
		end = ranges[0].End
		ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
		log.Info("Content-Range", zap.Int64("start", start), zap.Int64("end", end), zap.Int64("fileSize", file.FileSize))
		w.WriteHeader(http.StatusPartialContent)
	}
	contentLength := end - start + 1
	mimeType := file.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	ctx.Header("Content-Type", mimeType)
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))
	disposition := "inline"
	if ctx.Query("d") == "true" {
		disposition = "attachment"
	}
	ctx.Header("Content-Disposition", fmt.Sprintf("%s; filename=\"%s\"", disposition, file.FileName))
	if r.Method != "HEAD" {
		lr, _ := utils.NewTelegramReader(ctx, worker.Client, file.Location, start, end, contentLength)
		if _, err := io.CopyN(w, lr, contentLength); err != nil {
			log.Error("Error while copying stream", zap.Error(err))
		}
	}
}
