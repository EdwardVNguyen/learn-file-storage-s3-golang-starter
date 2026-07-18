package main

import (
	"fmt"
	"net/http"
	"io"
	"os"
	"strings"
	"path/filepath"
	"mime"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
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

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20 // 10 mb
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse multipart form", err)
		return
	}
	
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized to get video as current user is not owner", err)
		return
	}

	contentType := header.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType) 
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not parse mediatype", err)
		return
	}
	if !(mediaType == "image/jpeg" || mediaType == "image/png") {
		respondWithError(w, http.StatusBadRequest, "mediaType must be image/jpeg or image/png", fmt.Errorf("unexpected type, not image/jpeg or image/png"))
		return
	}


	fileExtension := strings.Split(contentType, "/")[1]
	name := fmt.Sprintf(`%s.%s`, videoIDString, fileExtension)
	path := filepath.Join(cfg.assetsRoot, name)

	endpoint := fmt.Sprintf(`http://localhost:%s/assets/%s.%s`, cfg.port, videoIDString, fileExtension)
	video.ThumbnailURL = &endpoint

	assetFile, err := os.Create(path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create asset file", err)
		return
	}

	if _, err := io.Copy(assetFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not copy file to asset file", err)
		return
	}

	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
