---
name: test-docker
description: Build and smoke test the FrankenAsync Docker image, including a 500-task concurrency test
user_invocable: true
---

# Test FrankenAsync Docker Image

Build the Docker image, start a container, and verify the demo page works with high concurrency.

## Steps

1. Build the image:
```bash
GITHUB_TOKEN=$(gh auth token) docker build --secret id=github_token,env=GITHUB_TOKEN -t frankenasync .
```
If `gh auth token` fails, build without the secret (slower):
```bash
docker build -t frankenasync .
```

2. Start the container:
```bash
docker rm -f frankenasync-test 2>/dev/null
docker run --rm -d --name frankenasync-test -p 8081:8081 frankenasync
```

3. Wait for startup, then verify the server log shows `Starting FrankenAsync server`:
```bash
sleep 5
docker logs frankenasync-test 2>&1 | tail -2
```

4. Test the landing page returns HTTP 200:
```bash
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:8081/?n=10&local=1")
echo "Landing page: HTTP $STATUS"
```

5. **Concurrency stress test** — run 500 parallel tasks. This tests the ARM64 connection abort fix (PAC/longjmp crash) and the Go semaphore sliding window:
```bash
STATUS=$(curl -s -o /dev/null -w "%{http_code}" --max-time 120 "http://localhost:8081/?n=500&local=1")
echo "500 tasks: HTTP $STATUS"
```

6. Test the local API endpoint:
```bash
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:8081/api/comments/1")
echo "API endpoint: HTTP $STATUS"
```

7. Check logs for panics, crashes, or SIGBUS (the ARM64 PAC issue):
```bash
docker logs frankenasync-test 2>&1 | grep -i "panic\|crash\|signal\|SIGBUS\|abort" | head -5
```

8. Tear down:
```bash
docker rm -f frankenasync-test
```

9. Report results: image size, thread/worker count, pages passed/failed, any errors. A PHP error about "Invalid duration format" in index.php is a known demo bug and not a test failure — the key test is that the server doesn't crash.
