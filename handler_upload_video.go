package main

import (
	"net/http"
	"mime"
	"os"
	"io"
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30 // 1 GB
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not get video", err)
		return
	}
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "User is not owner of video", err)
		return
	}
		
	videoFile, _, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not get form file from request", err)
		return
	}
	defer videoFile.Close()

	const contentType = "video/mp4"
	mediaType, _, err := mime.ParseMediaType(contentType); 
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not parse mediatype", err)
		return
	}

	const tempFileName = "tubely-upload.mp4"
	tempFile, err := os.CreateTemp("", tempFileName)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create temporary file", err)
		return
	}
	defer os.Remove(tempFileName)
	defer tempFile.Close()

if _, err := io.Copy(tempFile, videoFile); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not copy video file to temporary file", err)
		return
	}

	if _, err := tempFile.Seek(0,io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not reset tempFile starting pointer", err)
		return
	}

	key := make([]byte, 32)
	rand.Read(key)
	encodedStr := base64.RawURLEncoding.EncodeToString(key)

	fileExtension := strings.Split(contentType, "/")[1]
	s3Key := fmt.Sprintf(`%s.%s`, encodedStr, fileExtension)
	
	// https://docs.aws.amazon.com/code-library/latest/ug/go_2_s3_code_examples.html
	cfg.s3Client.PutObject( context.TODO(), &s3.PutObjectInput {
		Bucket: &cfg.s3Bucket,
		Key: &s3Key,
		Body: videoFile,
		ContentType: &mediaType,
	})

	updatedVideoURL := fmt.Sprintf(`https://%s.s3.%s.amazonaws.com/%s`, cfg.s3Bucket, cfg.s3Region, s3Key)
	video.VideoURL = &updatedVideoURL
	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video", err)
		return
	}
}

