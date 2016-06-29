 * [x] teleport command
 * [ ] passing teleport parameters
   * [ ] parsing and setting port number
 * [ ] client-side command handler
   * [ ] accepting local connections
   * [ ] getting back result 
   * [ ] handle errors
   * [ ] handle multiple connections simultaneously (or limit them)
   * [ ] wrap request over websocket to the server side
    * [ ] create websocket tunnel
    * [ ] forward request over websocket and listen for reply
   * [ ] write tests
 * [ ] server-side handler
  * [ ] check that port on container is open
    * [ ] wait or no wait
  * [ ] accept connection over the websocket
  * [ ] read from container port
    * [ ] send reply stream through websocket
  * [ ] handle errors, what to do (and how to pass errrors)
   * [ ] port is closed
   * [ ] port doesn't accept connections
   * [ ] wrapped connection is reset
   * [ ] client connection is reset
   * [ ] some websocket error
 * [ ] draw diagram how it is wrapped
