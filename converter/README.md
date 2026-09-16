# PubConverter

A tiny HTTP API that converts `.pub` (Microsoft Publisher) files to PDF using headless LibreOffice.

## Build

```bash
docker build -t pub-converter .
```

## Run

```bash
docker run -p 8080:8080 pub-converter
```

## Test

Convert a Publisher file to PDF:

```bash
curl -X POST -F "file=@test.pub" http://localhost:8080/convert -o output.pdf
```

## Health check

```bash
curl http://localhost:8080/health
# {"status": "ok"}
```

## API

| Endpoint     | Method  | Description                                        |
| ------------ | ------- | -------------------------------------------------- |
| `/convert`   | POST    | Multipart `file` field (.pub), returns `converted.pdf` |
| `/health`    | GET     | Health check                                       |

- Max upload: 50MB (413 if exceeded)
- Bad file type: 400
- Conversion failure: 500
- Conversion timeout (>120s): 504
- CORS enabled (`Access-Control-Allow-Origin: *`) with OPTIONS preflight support