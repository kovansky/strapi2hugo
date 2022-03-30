/*
 * Copyright (c) 2022.
 *
 * Originally created by F4 Developer (Stanisław Kowański). Released under GNU GPLv3 (see LICENSE)
 */

package aws

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/kovansky/midas"
	"os"
	"path/filepath"
	"strings"
)

type fileWalk chan string

func (f fileWalk) Walk(path string, info os.FileInfo, err error) error {
	if err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	f <- path
	return nil
}

type DeploymentSettigs struct {
	BucketName string `json:"bucketName"`
	Region     string `json:"region"`
	AccessKey  string `json:"accessKey"`
	SecretKey  string `json:"secretKey"`
	S3Prefix   string `json:"s3Prefix"`
}

type Deployment struct {
	site               midas.Site
	deploymentSettings midas.DeploymentSettings
	publicPath         string
}

func New(site midas.Site, deploymentSettings midas.DeploymentSettings) midas.Deployment {
	// Get build destination directory
	var publicPath string
	if site.OutputSettings.Build != "" {
		if filepath.IsAbs(site.OutputSettings.Build) {
			publicPath = site.OutputSettings.Build
		} else {
			publicPath = filepath.Join(site.RootDir, site.OutputSettings.Build)
		}
	} else {
		publicPath = filepath.Join(site.RootDir, "public")
	}

	return &Deployment{site: site, deploymentSettings: deploymentSettings, publicPath: publicPath}
}

// Deploy uploads built site to the AWS S3 bucket.
func (d *Deployment) Deploy() error {
	walker, err := d.retrieveFiles()
	if err != nil {
		return err
	}

	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(d.deploymentSettings.AWS.AccessKey, d.deploymentSettings.AWS.SecretKey, "")),
		config.WithRegion(d.deploymentSettings.AWS.Region))
	if err != nil {
		return err
	}

	// Upload each file to the S3 bucket.
	uploader := manager.NewUploader(s3.NewFromConfig(cfg))
	for path := range walker {
		err = func() error {
			rel, err := filepath.Rel(d.publicPath, path)

			if err != nil {
				return err
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer func() {
				_ = file.Close()
			}()

			if err = d.uploadFile(uploader, file, rel); err != nil {
				return err
			}

			return nil
		}()
		if err != nil {
			return err
		}
	}

	return nil
}

// ToDo: Removing old files from S3.
// ToDo: Cloudfront invalidation

// uploadFile uploads a file to the S3 bucket.
func (d *Deployment) uploadFile(uploader *manager.Uploader, file *os.File, rel string) error {
	fileKey := rel
	if d.deploymentSettings.AWS.S3Prefix != "" {
		fileKey = fmt.Sprintf("%s/%s", d.deploymentSettings.AWS.S3Prefix, rel)
	}

	fileKey = strings.ReplaceAll(fileKey, "\\", "/")

	contentType := getFileContentType(file.Name())

	_, err := uploader.Upload(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(d.deploymentSettings.AWS.BucketName),
		Key:         aws.String(fileKey),
		Body:        file,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return err
	}

	return nil
}

// reteiveFiles walks the public directory and returns a channel of files to be uploaded.
func (d *Deployment) retrieveFiles() (fileWalk, error) {
	walker := make(fileWalk)

	// Gather the files to upload by walking the path recursively.
	go func() {
		defer close(walker)
		if err := filepath.Walk(d.publicPath, walker.Walk); err != nil {
			panic(err)
		}
	}()

	return walker, nil
}

// getFileContentType returns the content type of the file based on the extension.
func getFileContentType(fileName string) string {
	typeByExtension := map[string]string{
		".html": "text/html",
		".css":  "text/css",
		".xml":  "text/xml",

		".js":  "application/javascript",
		".pdf": "application/pdf",

		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".webp": "image/webp",

		".webm": "video/webm",
		".mp4":  "video/mp4",
		".ogv":  "video/ogg",
		".avi":  "video/x-msvideo",

		".ogg":  "audio/ogg",
		".mp3":  "audio/mpeg",
		".mpeg": "audio/mpeg",
	}

	extension := filepath.Ext(fileName)

	if contentType, ok := typeByExtension[extension]; ok {
		return contentType
	} else {
		return "application/octet-stream"
	}
}
