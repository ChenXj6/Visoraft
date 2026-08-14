FROM python:3.12-slim-bookworm

LABEL org.opencontainers.image.title="Visoraft Local Fixture Provider" \
      org.opencontainers.image.licenses="Apache-2.0" \
      io.visoraft.fixture-only="true"

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

RUN groupadd --system --gid 10002 visoraft-fixture \
    && useradd --system --uid 10002 --gid visoraft-fixture --create-home visoraft-fixture

WORKDIR /app
COPY server.py /app/server.py

USER 10002:10002
EXPOSE 8090

CMD ["python", "/app/server.py"]
