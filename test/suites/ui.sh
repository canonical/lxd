test_ui() {
  echo "==> UI command starts a webserver that can be used to access the LXD API"

  # Start the UI command in the background and redirect output to a log file
  LOGFILE=/tmp/lxc-ui.log
  lxc ui > "$LOGFILE" &
  PID=$!

  # Wait until server is up and sent the URL to the log file
  while [ -z "${URL:-}" ]; do
    sleep 0.1
    URL=$(sed -n 's/^Web server running at: //p' "$LOGFILE" | head -n1)
  done

  # Query the API to check if the server is running and the auth status is trusted
  API_URL="${URL/\/ui/\/1.0}"
  AUTH_STATUS=$(curl -fsS "$API_URL" | jq -r ".metadata.auth")
  if [ ! "$AUTH_STATUS" = "trusted" ] ; then
    echo "invalid auth status"
    exit 1
  fi

  # Stop the UI command and clean up
  kill "$PID"
  rm -rf "$LOGFILE"
}
