package tcpwrite

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/project-flogo/core/activity"
	"github.com/project-flogo/core/data/metadata"
	"github.com/project-flogo/core/support/log"
)

var logger log.Logger
var delimiter byte
var connection net.Conn

// Activity ...
type Activity struct {
	settings *Settings
}

func init() {
	_ = activity.Register(&Activity{}, New)
}

var activityMd = activity.ToMetadata(&Settings{}, &Input{}, &Output{})

// Metadata returns the activity's metadata
func (a *Activity) Metadata() *activity.Metadata {
	return activityMd
}

// New ...
func New(ctx activity.InitContext) (activity.Activity, error) {
	settings := &Settings{}
	err := metadata.MapToStruct(ctx.Settings(), settings, true)
	if err != nil {
		return nil, err
	}
	activity := &Activity{}
	logger = ctx.Logger()
	if settings.Network == "" {
		settings.Network = "tcp"
	}
	if len(settings.CustomDelimiter) > 0 {
		// Refer this for delimiter hex codes: http://www.columbia.edu/kermit/ascii.html
		delimBytes, err := hex.DecodeString(settings.CustomDelimiter)
		if err != nil {
			return nil, err
		}
		r, _ := utf8.DecodeRune(delimBytes)
		delimiter = byte(r)
		logger.Debugf("Custom delimiter is set to: Decimal [%[1]v] or Hex [%[1]x]", delimiter)
	} else if len(settings.Delimiter) > 0 {
		switch settings.Delimiter {
		case "Carriage Return (CR)":
			delimiter = '\r'
		case "Line Feed (LF)":
			delimiter = '\n'
		case "Form Feed (FF)":
			delimiter = '\f'
		}
		logger.Debugf("Delimiter is set to: Decimal [%[1]v] or Hex [%[1]x]", delimiter)
	}
	activity.settings = settings
	return activity, nil
}

// Cleanup ...
func (a *Activity) Cleanup() error {
	if connection == nil || a.settings.KeepConnectionOpen {
		return nil
	}
	logger.Info("Closing connection")
	err := connection.Close()
	if err != nil {
		return err
	}
	return nil
}

// Eval ...
func (a *Activity) Eval(ctx activity.Context) (done bool, err error) {
	logger.Debug("Executing TCP Write activity")
	input := &Input{}
	err = ctx.GetInputObject(input)
	if err != nil {
		return false, err
	}
	err = initConnection(*a.settings, input.Connection)
	if err != nil {
		return false, err
	}
	message := input.Data
	if delimiter > 0 {
		logger.Debugf("Appending message delimiter: [%+v]", delimiter)
		message = append(message, delimiter)
	}
	output := &Output{}
	output.BytesWritten, err = connection.Write(message)
	if err != nil {
		logger.Errorf("Unable to write the data! %v", err)
		return false, err
	}
	logger.Infof("Written %d bytes", output.BytesWritten)
	if a.settings.WaitForReply {
		output.Data, output.BytesReceived = readData(connection)
		logger.Infof("Received %d bytes", output.BytesReceived)
	}
	if a.settings.KeepConnectionOpen {
		output.Connection = connection
	}
	ctx.SetOutputObject(output)
	return true, nil
}

func initConnection(settings Settings, existingConn interface{}) error {
	if connection != nil {
		return nil
	}
	logger.Debugf("Dialing connection to %s network...", settings.Network)
	var err error
	var ok bool
	if settings.KeepConnectionOpen && existingConn != nil {
		connection, ok = existingConn.(net.Conn)
		if !ok {
			err = errors.New("Unable to read existing connection")
			logger.Error(err)
			return err
		}
	} else {
		address := fmt.Sprintf("%s:%s", settings.Host, settings.Port)
		connection, err = net.Dial(settings.Network, address)
		if err != nil {
			logger.Errorf("Unable to dial the connection! Caused by %v", err)
			return err
		}
	}
	if settings.WriteTimeoutMs > 0 {
		deadline := time.Now().Add(time.Millisecond * time.Duration(settings.WriteTimeoutMs))
		connection.SetWriteDeadline(deadline)
		logger.Debugf("Write timeout is set to %d milliseconds", settings.WriteTimeoutMs)
	}
	logger.Infof("Connected to %s network [%s:%s]", settings.Network, settings.Host, settings.Port)
	return nil
}

func readData(conn net.Conn) ([]byte, int) {
	if delimiter != 0 {
		data, err := bufio.NewReader(conn).ReadBytes(delimiter)
		if err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") {
				logger.Errorf("Error reading data from connection: %v", err)
			} else {
				logger.Info("Connection is closed.")
			}
			if nerr, ok := err.(net.Error); !ok || !nerr.Timeout() {
				// Return if not timeout error
				return nil, len(data)
			}
		}
		return data[:len(data)-1], len(data)
	}
	var buf bytes.Buffer
	_, err := io.Copy(&buf, conn)
	if err != nil {
		if !strings.Contains(err.Error(), "use of closed network connection") {
			logger.Errorf("Error reading data from connection: %v", err)
		} else {
			logger.Info("Connection is closed.")
		}
		if nerr, ok := err.(net.Error); !ok || !nerr.Timeout() {
			// Return if not timeout error
			return nil, 0
		}
	}
	data := buf.Bytes()
	return data, len(data)
}
