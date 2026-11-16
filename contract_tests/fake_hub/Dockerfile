# The fake Hugging Face Hub (test tooling; stdlib only). /hubsecrets holds the placeholder token file the fake runner
# reads (RUNNER_HF_TOKEN_FILE); a named volume mounted there keeps this owner.
FROM python:3.14.10-slim-trixie
WORKDIR /app
RUN useradd --system --uid 10003 fake && mkdir /hubsecrets && chown fake /hubsecrets
COPY server.py .
USER fake
EXPOSE 8000
HEALTHCHECK --interval=3s --timeout=3s --retries=20 CMD ["python", "server.py", "call", "GET", "/__manifest"]
CMD ["python", "server.py"]
