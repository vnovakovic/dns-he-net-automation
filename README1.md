  ---
  1. Compile Linux binary

  cd C:/Users/vladimir/Documents/Development/dns-he-net-automation

  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -o dnshenet-server-linux-amd64 ./cmd/server

  2. Compile Windows binary

  cd C:/Users/vladimir/Documents/Development/dns-he-net-automation

  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -o dnshenet-server-windows-amd64.exe ./cmd/server

  ---
  3. Windows Installer (Inno Setup) — step by step

  Requires https://jrsoftware.org/ispage.php installed.

  # Step 1 — compile the Windows binary (if not done above)
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -o dnshenet-server.exe ./cmd/server

  # Step 2 — run ISCC with version injected
  VERSION=0.1.0
  "C:/Users/vladimir/AppData/Local/Programs/Inno Setup 6/ISCC.exe" \
    /DMyAppVersion=$VERSION \
    installer/dnshenet-server.iss

  # Output: dnshenet-server-installer.exe in the project root

  ---
  4. Docker — no docker-compose.yaml exists yet

  The repo has a Dockerfile but no docker-compose.yaml. Here are the equivalent manual Docker commands:

  # Step 1 — build the image
  docker build -t dns-he-net-automation:0.1.0 .

  # Step 2 — create a data directory for persistent storage
  mkdir -p C:/dnshenet-data

  # Step 3 — run the container
  docker run -d \
    --name dns-he-net \
    --restart unless-stopped \
    -p 9001:9001 \
    -v C:/dnshenet-data:/data \
    -e JWT_SECRET="your-random-32-char-secret-here" \
    -e ADMIN_USERNAME=admin \
    -e ADMIN_PASSWORD=admin123 \
    -e TOKEN_RECOVERY_ENABLED=true \
    dns-he-net-automation:0.1.0

  # Useful commands
  docker logs dns-he-net          # view logs
  docker stop dns-he-net          # stop
  docker start dns-he-net         # start
  docker rm dns-he-net            # remove container

  ---


