"""Compatibility routes that are still owned by the Easel runtime."""

import os

from fastapi.responses import JSONResponse

from easel.runtime_env import web_environment


def load_app():
    os.environ.update(web_environment(dict(os.environ)))
    from web.app import app

    return app


app = load_app()
app.router.routes[:] = [
    route
    for route in app.router.routes
    if getattr(route, "path", "") not in {"/api/ideas", "/api/ideas/{iid}"}
]


@app.get("/health")
async def health():
    return JSONResponse({"status": "ok"})
