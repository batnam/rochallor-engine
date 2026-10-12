FROM python:3.12-slim
WORKDIR /app
COPY workflow-sdk-python/dist /candidate
RUN cd /candidate && sha256sum -c SHA256SUMS && pip install --no-cache-dir ./*.whl && pip check
COPY e2e/workers/python/worker.py .
CMD ["python", "worker.py"]
