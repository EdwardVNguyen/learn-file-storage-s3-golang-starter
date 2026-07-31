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
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// set request upload limit to 1 GB
	const uploadLimit = 1 << 30 
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

	// in order to update video's metadata later on
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	// authenticate user
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

	// ok, we get the video metadata from the database (we also validate if the video is from the user)
	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not get video", err)
		return
	}
	if userID != video.UserID {
		respondWithError(w, http.StatusUnauthorized, "User is not owner of video", err)
		return
	}
		
	// formfile gets the first file that has the form key "video"
	videoFile, videoFileHeader, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could not get form file from request", err)
		return
	}
	defer videoFile.Close()

	// validate that the file is indeed of mediatype video/mp4
	contentType := videoFileHeader.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType); 
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not parse mediatype", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "media type is not video/mp4 so invalid", err)
		return
	}

	// We make a temporary file since formfile does not give us the operations needed so that we can send the video file to aws
	// so we have to copy the video file in a temporary file which does give us the operations and info needed
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

	// reset temp file pointer since its EOF, we want to point it back to the start of the file so that our we can send the file onto the bucket
	if _, err := tempFile.Seek(0,io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not reset tempFile starting pointer", err)
		return
	}

	// make a unique arbitrary name for the user's video
	key := make([]byte, 32)
	rand.Read(key)
	encodedStr := base64.RawURLEncoding.EncodeToString(key)

	// determine if it is landscape, portrait, or other
	var prefix string
	aspectRatio, err := getVideoAspectRatio(tempFile.Name()) 
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not get aspect ratio", err)
		return
	}

	switch aspectRatio {
	case "16:9":
		prefix = "landscape"
	case "9:16":
		prefix = "portrait"
	default:
		prefix = "other"
	}

	fileExtension := strings.Split(contentType, "/")[1]
	s3Key := fmt.Sprintf(`%s/%s.%s`, prefix, encodedStr, fileExtension)

	// processed version of video from the temporary file
	tempFilePath, err:= filepath.Abs(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting file path from temporary file", err)
		return
	}
	processedVideoFilePath, err := processVideoForFastStart(tempFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing video for fast start", err)
		return
	}
	processedVideoFile, err := os.Open(processedVideoFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error opening file from processed video file path", err)
		return
	}
	defer processedVideoFile.Close();
	
	// put the file onto aws s3 bucket
	// https://docs.aws.amazon.com/code-library/latest/ug/go_2_s3_code_examples.html
	if _, err := cfg.s3Client.PutObject( context.TODO(), &s3.PutObjectInput {
			Bucket: &cfg.s3Bucket,
			Key: &s3Key,
			Body: processedVideoFile,
			ContentType: &mediaType,
		}); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to bucket", err)		
		return
	}

	// update the video's metadata
	// updatedVideoURL := fmt.Sprintf(`https://%s.s3.%s.amazonaws.com/%s`, cfg.s3Bucket, cfg.s3Region, s3Key)
	updatedVideoURL := fmt.Sprintf(`%s,%s`, cfg.s3Bucket, s3Key)
	video.VideoURL = &updatedVideoURL
	
	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not update video", err)
		return
	}

	video, err = cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error converting video to signed video", err)
		return
	}
	respondWithJSON(w, http.StatusOK, video)
}

