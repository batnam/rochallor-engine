FROM python:3.12-slim

WORKDIR /app

# Install SDK from source
COPY workflow-sdk-python /sdk
RUN pip install --no-cache-dir /sdk

# Copy worker script
COPY e2e/workers/python/worker.py .

CMD ["python", "worker.py"]
