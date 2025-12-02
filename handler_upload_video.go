package main

import (
	"net/http"
	"fmt"
	"mime"
	"os"
	"io"
	"context"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)

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

	fmt.Println("uploading video", videoID, "by user", userID)

	v, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't find video", err)
		return
	}
	if userID != v.UserID {
		respondWithError(w, http.StatusUnauthorized, "You're not an owner of this video", err)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse video", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Could't parse mediatype", nil)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file format for video", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't create temporary file", nil)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying to temporary file", nil)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't reset temp file's pointer", nil)
		return
	}

	assetPath := getAssetPath(mediaType)
	input := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &assetPath,
		Body:        tempFile,
		ContentType: &mediaType,
	}
	_, err = cfg.s3Client.PutObject(context.Background(), &input)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't put the video into S3", nil)
		return
	}

	url := cfg.getVideoURL(assetPath)
	v.VideoURL = &url

	err = cfg.db.UpdateVideo(v)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't update video url", err)
		return
	}

	respondWithJSON(w, http.StatusOK, v)
}
