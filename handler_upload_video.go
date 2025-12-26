package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
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
		respondWithError(w, http.StatusInternalServerError, fmt.Sprintf("Could't get the ratio of the video"), err)
		return
	}

	directory := ""
	if ratio == "16:9" {
		directory = "landscape"
	} else if ratio == "9:16" {
		directory = "portrait"
	} else {
		directory = "other"
	}

	key := getAssetPath(mediaType)
	key = path.Join(directory, key)

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't process the video for fast start", err)
		return
	}
	defer os.Remove(processedFilePath)

	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't open processed video file", err)
		return
	}
	defer processedFile.Close()

	input := s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(key),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	}
	_, err = cfg.s3Client.PutObject(r.Context(), &input)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't upload the video to S3", err)
		return
	}

	//url := cfg.getVideoURL(key) // NOTE: Change this

	url := cfg.s3Bucket + "," + key
	v.VideoURL = &url

	err = cfg.db.UpdateVideo(v)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't update video", err)
		return
	}

	v, err = cfg.dbVideoToSignedVideo(v)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could't get the signed video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, v)
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, nil
	}

	splitted := strings.Split(*video.VideoURL, ",")

	if len(splitted) < 2 {
		return video, nil
	}

	bucket := splitted[0]
	key := splitted[1]

	url, err := generatePresignedURL(cfg.s3Client, bucket, key, 10*time.Second)
	if err != nil {
		return video, err
	}

	video.VideoURL = aws.String(url)
	return video, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)

	params := s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}

	presignedReq, err := presignClient.PresignGetObject(context.Background(), &params, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", fmt.Errorf("error creating presigned http request")
	}

	return presignedReq.URL, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	processedFilePath := filePath + ".processing"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-movflags", "faststart", "-codec", "copy", "-f", "mp4", processedFilePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error processing video: %s, %v", stderr.String(), err)
	}

	fileInfo, err := os.Stat(processedFilePath)
	if err != nil {
		return "", fmt.Errorf("could not stat processed file: %v", err)
	}
	if fileInfo.Size() == 0 {
		return "", fmt.Errorf("processed file is empty")
	}

	return processedFilePath, nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var output bytes.Buffer
	cmd.Stdout = &output
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("Failed to run the command: %v", err)
	}

	var cmdOutput struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	err = json.Unmarshal(output.Bytes(), &cmdOutput)
	if err != nil {
		return "", fmt.Errorf("error unmarshaling the cmd output: %v", err)
	}

	if len(cmdOutput.Streams) == 0 {
		return "", errors.New("no video streams found")
	}

	width := cmdOutput.Streams[0].Width
	height := cmdOutput.Streams[0].Height

	if width == 16*height/9 {
		return "16:9", nil
	} else if height == 16*width/9 {
		return "9:16", nil
	}
	return "other", nil
}
