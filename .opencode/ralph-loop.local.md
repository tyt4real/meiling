---
active: true
iteration: 0
maxIterations: 100
---

"implement the full S3 spec: https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html , write a test for each case."

PLAN:
1. Reconcile existing S3 operations against the full S3 API spec (buckets + objects + multipart)
2. Identify missing operations
3. Implement each missing operation with tests
4. Verify full build + test suite
