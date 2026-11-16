# octoconv-worker-document

Document worker for OctoConv. One process serves one approved manifest over Worker Protocol v1.

The process reads `DOCUMENT_PROTOCOL_CORE_URL`, `DOCUMENT_PROTOCOL_CLIENT_CERT`, `DOCUMENT_PROTOCOL_CLIENT_KEY`, `DOCUMENT_PROTOCOL_CLIENT_CA`, `DOCUMENT_PROTOCOL_WORKER_ID`, and `DOCUMENT_PROTOCOL_MANIFEST_PATH`. It refuses to start when `DATABASE_URL` or `REDIS_ADDR` is set.

`schema/manifest-document-convert.json` is capability `octoconv.convert.document`. It names the twelve LibreOffice pairs: docx, odt, xlsx, ods, pptx, and odp to pdf, plus docx↔odt, xlsx↔ods, and pptx↔odp.

`soffice` must be on `PATH`. This process does not apply Landlock. The in-repo document worker does. Do not move document jobs onto this process until that confinement is measured.
