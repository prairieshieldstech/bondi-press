FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libreoffice libreoffice-writer libreoffice-calc libreoffice-impress \
    libreoffice-draw libreoffice-common \
    python3 python3-pip \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --break-system-packages --no-cache-dir pdf2docx

WORKDIR /app
COPY server.py .
EXPOSE 8080
CMD ["python3", "server.py"]