package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ExecStream ejecuta cmd en un contenedor enviando su salida estándar a
// stdout y la de error a stderr (sin mezclarlas: stdout puede ser un volcado
// binario). Con stdin, se le envía como entrada estándar. Devuelve el
// código de salida del comando.
func (c *Client) ExecStream(ctx context.Context, container string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	var created struct {
		ID string `json:"Id"`
	}
	body := map[string]any{"Cmd": cmd, "AttachStdin": stdin != nil, "AttachStdout": true, "AttachStderr": true}
	if err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", body, &created); err != nil {
		return 0, err
	}
	start := `{"Detach":false,"Tty":false}`

	var out io.ReadCloser
	if stdin == nil {
		resp, err := c.raw(ctx, c.stream, http.MethodPost, "/exec/"+created.ID+"/start", strings.NewReader(start), "application/json")
		if err != nil {
			return 0, err
		}
		out = resp.Body
	} else {
		conn, err := c.hijack(ctx, "/exec/"+created.ID+"/start", start)
		if err != nil {
			return 0, err
		}
		out = conn
		// La entrada se envía mientras se lee la salida; al acabar se cierra
		// la escritura para que el comando reciba EOF.
		go func() {
			_, _ = io.Copy(conn, stdin)
			if cw, ok := conn.Conn.(interface{ CloseWrite() error }); ok {
				_ = cw.CloseWrite()
			}
		}()
	}
	stop := context.AfterFunc(ctx, func() { out.Close() })
	defer stop()
	err := demuxTo(out, stdout, stderr)
	out.Close()
	if ctx.Err() != nil {
		return 0, ctx.Err() // cancelado o fuera de tiempo: la salida está incompleta
	}
	if err != nil {
		return 0, err
	}

	var info struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	if err := c.do(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, &info); err != nil {
		return 0, err
	}
	return info.ExitCode, nil
}

// hijackedConn es la conexión tras el "101 Switching Protocols": lo que
// quedó en el búfer se lee antes que la conexión.
type hijackedConn struct {
	net.Conn
	r *bufio.Reader
}

func (h *hijackedConn) Read(p []byte) (int, error) { return h.r.Read(p) }

// hijack abre la conexión bidireccional de Docker para enviar stdin.
func (c *Client) hijack(ctx context.Context, path, body string) (*hijackedConn, error) {
	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "http://docker/"+apiVersion+path, strings.NewReader(body))
	if err != nil {
		conn.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		defer conn.Close()
		var e struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&e)
		return nil, &APIError{Status: resp.StatusCode, Message: e.Message}
	}
	return &hijackedConn{Conn: conn, r: br}, nil
}

// demuxTo separa el flujo multiplexado de Docker en stdout y stderr.
func demuxTo(r io.Reader, stdout, stderr io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		w := stdout
		switch hdr[0] {
		case 2:
			w = stderr
		case 0, 1:
		default:
			return fmt.Errorf("flujo de Docker inesperado (tipo %d)", hdr[0])
		}
		if _, err := io.CopyN(w, r, int64(binary.BigEndian.Uint32(hdr[4:]))); err != nil {
			return err
		}
	}
}
