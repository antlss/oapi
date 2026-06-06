// Package docsui provides ready-to-serve HTML pages for browsing the generated
// OpenAPI document in a browser. All pages load the spec from /openapi.json, so
// any example command exposes them by serving these strings next to the spec:
//
//	GET /            -> IndexPage   (landing page, links to both UIs)
//	GET /redoc       -> RedocPage   (read-optimised reference)
//	GET /swagger     -> SwaggerPage (interactive "Try it out")
//	GET /openapi.json-> the spec (oapi adapter SpecHandler)
//
// The UIs are loaded from a CDN at pinned versions, so the examples need no
// embedded assets or extra Go dependencies.
package docsui

// ContentType is the value to send for all three pages.
const ContentType = "text/html; charset=utf-8"

// IndexPage is a small landing page linking to both documentation UIs.
const IndexPage = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Catalog API — Docs</title>
  <style>
    body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;margin:0;
         min-height:100vh;display:grid;place-items:center;background:#0f172a;color:#e2e8f0}
    .card{text-align:center;padding:2rem}
    h1{margin:0 0 .25rem;font-size:1.8rem}
    p{color:#94a3b8;margin:0 0 1.75rem}
    a.btn{display:inline-block;margin:.4rem;padding:.8rem 1.4rem;border-radius:.6rem;
          background:#1e293b;color:#e2e8f0;text-decoration:none;border:1px solid #334155}
    a.btn:hover{background:#334155}
    a.spec{display:block;margin-top:1.25rem;color:#64748b;font-size:.85rem}
  </style>
</head>
<body>
  <div class="card">
    <h1>Catalog API</h1>
    <p>OpenAPI 3 documentation</p>
    <a class="btn" href="/swagger">Swagger UI — try it out</a>
    <a class="btn" href="/redoc">Redoc — reference</a>
    <a class="spec" href="/openapi.json">/openapi.json</a>
  </div>
</body>
</html>`

// RedocPage renders the spec with Redoc (clean, read-only reference docs).
const RedocPage = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Catalog API — Redoc</title>
  <style>body { margin: 0; padding: 0; }</style>
</head>
<body>
  <redoc spec-url="/openapi.json"></redoc>
  <script src="https://cdn.jsdelivr.net/npm/redoc@2.1.5/bundles/redoc.standalone.js"
          integrity="sha384-0GrsyTQc9Oqd8h+b2dbc4XdR2T/DYpy0tLNNstyx+LBMUyiBbcWPbEs9aRmUcaxD"
          crossorigin="anonymous"></script>
</body>
</html>`

// SwaggerPage renders the spec with Swagger UI, which lets you call the API
// directly from the page ("Try it out").
const SwaggerPage = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Catalog API — Swagger UI</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css"
        integrity="sha384-wxLW6kwyHktdDGr6Pv1zgm/VGJh99lfUbzSn6HNHBENZlCN7W602k9VkGdxuFvPn"
        crossorigin="anonymous">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"
          integrity="sha384-wmyclcVGX/WhUkdkATwhaK1X1JtiNrr2EoYJ+diV3vj4v6OC5yCeSu+yW13SYJep"
          crossorigin="anonymous"></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: '/openapi.json',
        dom_id: '#swagger-ui',
        deepLinking: true,
        tryItOutEnabled: true,
      });
    };
  </script>
</body>
</html>`
