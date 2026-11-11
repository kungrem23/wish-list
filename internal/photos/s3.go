package photos

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const objectPrefix = "gift-photos/"

var ErrNotFound = errors.New("photo object not found")

type Store struct {
	client *s3.Client
	bucket string
}

type Object struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
}

func FromEnv() (*Store, error) {
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		return nil, nil
	}
	bucket, accessKey, secretKey := os.Getenv("S3_BUCKET"), os.Getenv("S3_ACCESS_KEY_ID"), os.Getenv("S3_SECRET_ACCESS_KEY")
	if bucket == "" || accessKey == "" || secretKey == "" {
		return nil, errors.New("S3_BUCKET, S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required with S3_ENDPOINT")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("S3_ENDPOINT must be an HTTP(S) endpoint without credentials, query or fragment")
	}
	region := os.Getenv("S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return &Store{bucket: bucket, client: s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Region:       region,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: accessKey, SecretAccessKey: secretKey}, nil
		}),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})}, nil
}

func (s *Store) Put(ctx context.Context, id, contentType string, size int64, body io.ReadSeeker) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(objectPrefix + id),
		ContentType: aws.String(contentType), ContentLength: aws.Int64(size), Body: body,
	})
	return err
}

func (s *Store) Get(ctx context.Context, id string) (Object, error) {
	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(objectPrefix + id),
	})
	var missing *types.NoSuchKey
	if errors.As(err, &missing) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, err
	}
	return Object{Body: result.Body, ContentType: aws.ToString(result.ContentType), Size: aws.ToInt64(result.ContentLength)}, nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(objectPrefix + id),
	})
	return err
}
