package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/aws"
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
		respondWithError(w, http.StatusBadRequest, "Could't parse mediatype", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file format for video", err)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't create temporary file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying to temporary file", err)
		return
	}

	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't reset temp file's pointer", err)
		return
	}

	ratio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Could't get the ratio of the video: %v", err), err)
		return
	}

	prefix := ""
	if ratio == "16:9" {
		prefix = "landscape"
	} else if ratio == "9:16" {
		prefix = "portrait"
	} else {
		prefix = "other"
	}

	assetPath := prefix + "/" + getAssetPath(mediaType)
	input := s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(assetPath),
		Body:        tempFile,
		ContentType: aws.String(mediaType),
	}
	_, err = cfg.s3Client.PutObject(r.Context(), &input)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't put the video into S3", err)
		return
	}

	url := cfg.getVideoURL(assetPath)
	v.VideoURL = &url

	err = cfg.db.UpdateVideo(v)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, v)
}
