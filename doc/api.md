# Main API specification

<link rel="stylesheet" type="text/css" href="../_static/swagger-ui.css" ></link>
<div id="swagger-ui"></div>

<script src="../_static/swagger-ui-bundle.js" charset="UTF-8"> </script>
<script src="../_static/swagger-ui-standalone-preset.js" charset="UTF-8"> </script>
<script>
window.onload = function() {
  // Begin Swagger UI call region
  const ui = SwaggerUIBundle({
    url: "https://raw.githubusercontent.com/lxc/lxd/master/doc/rest-api.yaml",
    dom_id: '#swagger-ui',
    deepLinking: true,
    presets: [
      SwaggerUIBundle.presets.apis,
      SwaggerUIStandalonePreset
    ],
    plugins: [],
    validatorUrl: "none",
    defaultModelsExpandDepth: -1,
    supportedSubmitMethods: []
  })
  // End Swagger UI call region

  window.ui = ui
}
</script>
