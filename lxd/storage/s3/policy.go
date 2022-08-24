package s3

import (
	"encoding/json"
	"fmt"
)

// BucketPolicy generates an S3 bucket policy for role.
func BucketPolicy(bucketName string, roleName string) (json.RawMessage, error) {
	switch roleName {
	case "admin":
		return []byte(fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": [
					"s3:*"
				],
				"Resource": [
					"arn:aws:s3:::%s/*"
				]
			}]
		}`, bucketName)), nil
	case "read-only":
		return []byte(fmt.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [{
				"Effect": "Allow",
				"Action": [
					"s3:ListBucket",
					"s3:GetBucketLocation",
					"s3:GetObject",
					"s3:GetObjectVersion"
				],
				"Resource": [
					"arn:aws:s3:::%s/*"
				]
			}]
		}`, bucketName)), nil
	}

	return nil, fmt.Errorf("Invalid key role")
}
