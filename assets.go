package main

import (
	"os"
	"os/exec"
	"bytes"
	"io"
	"encoding/json"
	"time"
	"context"
	"strings"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	// store cmd.Stdout in buffer
	// https://stackoverflow.com/questions/23454940/getting-bytes-buffer-does-not-implement-io-writer-error-message
	var b bytes.Buffer
	cmd.Stdout = io.Writer(&b) 

	if err := cmd.Run(); err != nil {
		return "", err
	}

	type Stream struct {
		Width float32 `json:"width"`
		Height float32 `json:"height"`
		Type string `json:"codec_type"`
	}
	type probeResult struct {
		Streams []Stream `json:"streams"`
	}
	var result probeResult 
	var videoRatio float32

	if err := json.Unmarshal(b.Bytes(), &result); err != nil {
		return "", err
	}

	for _, s := range result.Streams {
		if s.Type == "video" {
			videoRatio = s.Width / s.Height		
		}
	}

	const (
		landscapeRatio = 16.0 / 9.0
		portraitRatio = 9.0 / 16.0
		offset = 0.5 // our tolerance level
	)

	if (videoRatio >= landscapeRatio - offset) && (videoRatio <= landscapeRatio + offset) {
		return "16:9", nil
	} else if (videoRatio >= portraitRatio - offset) && (videoRatio <= portraitRatio + offset) {
		return "9:16", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	if err := cmd.Run(); err != nil {
		return "", err;
	}
	return outputFilePath, nil;
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignedClient := s3.NewPresignClient(s3Client)
	presignedHTTPRequest, err := presignedClient.PresignGetObject( context.TODO(), &s3.GetObjectInput {
			Bucket: &bucket,
			Key: &key,
		}, s3.WithPresignExpires(expireTime),
	)
	if err != nil {
		return "", err
	}

	return presignedHTTPRequest.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}
	arr := strings.Split(*video.VideoURL, ",")	
	if len(arr) != 2 {
		return database.Video{}, fmt.Errorf("must need bucket and key, but arr length is not 2")
	}
	bucket := arr[0]
	key := arr[1]
	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, 15 * time.Minute)
	if err != nil {
		return database.Video{}, err
	}
	video.VideoURL = &presignedURL

	return video, nil
}
