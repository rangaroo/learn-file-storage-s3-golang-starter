package main

import (
	"fmt"
	"io"
    "os"
	"net/http"

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

	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType := header.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Missing 'Content-Type' header for thumbnail", nil)
		return
	}

    assetPath := getAssetPath(videoID, mediaType)
    assetDiskPath := cfg.getAssetDiskPath(assetPath)

    dst, err := os.Create(assetDiskPath)
    if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't create file on the server", nil)
		return
    }
    defer dst.Close()
    if _, err = io.Copy(dst, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving file", err)
		return
	}

	v, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't find video", err)
		return
	}
	if userID != v.UserID {
		respondWithError(w, http.StatusUnauthorized, "You're not an owner of this video", err)
		return
	}

	url := cfg.getAssetURL(assetPath)
	v.ThumbnailURL = &url

	err = cfg.db.UpdateVideo(v)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't update video thumbnail", err)
		return
	}

	respondWithJSON(w, http.StatusOK, v)
}
