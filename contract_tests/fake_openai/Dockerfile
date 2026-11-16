# The fake OpenAI-compatible server (test tooling; stdlib only). openssl comes with ca-certificates in the slim image.
FROM python:3.14.10-slim-trixie
WORKDIR /app
RUN useradd --system --uid 10002 fake && mkdir /certs && chown fake /certs
COPY server.py .
USER fake
EXPOSE 8000 8443
HEALTHCHECK --interval=3s --timeout=3s --retries=20 CMD ["python", "server.py", "call", "GET", "/v1/models"]
CMD ["python", "server.py"]
