package awskit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"code.olapie.com/conv"
	"code.olapie.com/errors"
	"code.olapie.com/ola/httpkit"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	cacheControl = "public, max-age=14400"
)

type S3ACL string

// S3Bucket ACL
const (
	S3Private                S3ACL = "private"
	S3PublicRead             S3ACL = "public-read"
	S3PublicReadWrite        S3ACL = "public-read-write"
	S3AWSExecRead            S3ACL = "aws-exec-read"
	S3AuthenticatedRead      S3ACL = "authenticated-read"
	S3BucketOwnerRead        S3ACL = "bucket-owner-read"
	S3BucketOwnerFullControl S3ACL = "bucket-owner-full-control"
)

type S3Bucket struct {
	bucket             string
	client             *s3.Client
	objExistsWaiter    *s3.ObjectExistsWaiter
	objNotExistsWaiter *s3.ObjectNotExistsWaiter
	ACL                types.ObjectCannedACL
	CacheControl       string
}

func NewS3Bucket(cfg aws.Config, bucket string, options ...func(*s3.Options)) *S3Bucket {
	b := &S3Bucket{
		bucket:       bucket,
		client:       s3.NewFromConfig(cfg, options...),
		ACL:          types.ObjectCannedACLPrivate,
		CacheControl: cacheControl,
	}
	b.objExistsWaiter = s3.NewObjectExistsWaiter(b.client)
	b.objNotExistsWaiter = s3.NewObjectNotExistsWaiter(b.client)
	return b
}

func (s *S3Bucket) Put(ctx context.Context, id string, content []byte, metadata map[string]string) error {
	input := &s3.PutObjectInput{
		Bucket:       aws.String(s.bucket),
		Key:          aws.String(id),
		Body:         bytes.NewBuffer(content),
		ACL:          s.ACL,
		CacheControl: aws.String(s.CacheControl),
		ContentType:  aws.String(httpkit.DetectMIMEType(content)),
		Metadata:     metadata,
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3.PutObject: %w", err)
	}
	return nil
}

func (s *S3Bucket) Get(ctx context.Context, id string) ([]byte, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(id),
	}
	output, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("s3.GetObject: %w", err)
	}

	content, err := io.ReadAll(output.Body)
	if err != nil {
		return nil, fmt.Errorf("io.ReadAll: %w", err)
	}
	output.Body.Close()
	return content, nil
}

func (s *S3Bucket) Exists(ctx context.Context, id string) (bool, error) {
	_, err := s.getHeadObject(ctx, id)
	err = errors.Cause(err)
	if err == nil {
		return true, nil
	}

	if apiErr, ok := err.(smithy.APIError); ok {
		if apiErr.ErrorCode() == "NotFound" {
			return false, nil
		}
	}
	return false, err
}

func (s *S3Bucket) Delete(ctx context.Context, id string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(id),
	})

	if err != nil {
		return fmt.Errorf("s3.DeleteObject: %w", err)
	}

	err = s.objNotExistsWaiter.Wait(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(id),
	}, time.Second*5)
	if err != nil {
		return fmt.Errorf("s3.ObjectNotExistsWaiter.Wait: %w", err)
	}
	return nil
}

func (s *S3Bucket) BatchDelete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	input := &s3.DeleteObjectsInput{
		Bucket: aws.String(s.bucket),
		Delete: &types.Delete{
			Objects: conv.MustSlice(ids, func(id string) types.ObjectIdentifier {
				return types.ObjectIdentifier{
					Key: aws.String(id),
				}
			}),
		},
	}

	output, err := s.client.DeleteObjects(ctx, input)
	if err != nil {
		return fmt.Errorf("s3.DeleteObjects: %w", err)
	}

	if len(output.Deleted) == 0 {
		return nil
	}

	if len(output.Deleted) != len(ids) {
		idSet := conv.MustSliceToSet[string, string](ids, nil)
		for _, del := range output.Deleted {
			delete(idSet, *del.Key)
		}
		if len(idSet) != 0 {
			return fmt.Errorf("some ids cannot be deleted: %v", conv.GetMapKeys(idSet))
		}
	}

	err = s.objNotExistsWaiter.Wait(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(ids[0]),
	}, time.Second*5)
	if err != nil {
		return fmt.Errorf("s3.ObjectNotExistsWaiter.Wait: %w", err)
	}
	return nil
}

func (s *S3Bucket) GetMetadata(ctx context.Context, id string) (map[string]string, error) {
	head, err := s.getHeadObject(ctx, id)
	if err != nil {
		return nil, err
	}
	return head.Metadata, nil
}

func (s *S3Bucket) getHeadObject(ctx context.Context, id string) (*s3.HeadObjectOutput, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(id),
	}
	return s.client.HeadObject(ctx, input)
}
