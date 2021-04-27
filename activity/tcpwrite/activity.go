package tcpwrite

import (
	"bufio"
	"bytes"
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

// Activity ...
type Activity struct {
	settings   *Settings
	connection net.Conn
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
	logger.Debugf("Dialing connection to %s network...", settings.Network)
	activity.connection, err = net.Dial(settings.Network, fmt.Sprintf("%s:%s", settings.Host, settings.Port))
	if err != nil {
		logger.Errorf("Unable to dial the connection! Caused by %s", err.Error())
		return nil, err
	}
	if settings.WriteTimeoutMs != 0 {
		deadline := time.Now().Add(time.Millisecond * time.Duration(settings.WriteTimeoutMs))
		activity.connection.SetWriteDeadline(deadline)
		logger.Debugf("Write timeout is set to %d milliseconds", settings.WriteTimeoutMs)
	}
	logger.Infof("Connected to %s network [%s:%s]", settings.Network, settings.Host, settings.Port)
	if len(settings.CustomDelimiter) > 0 {
		r, _ := utf8.DecodeRuneInString(settings.CustomDelimiter)
		delimiter = byte(r)
		logger.Debugf("Custom delimiter is set to: [%+v]", delimiter)
	} else if len(settings.Delimiter) > 0 {
		switch settings.Delimiter {
		case "Carriage Return (CR)":
			delimiter = '\r'
		case "Line Feed (LF)":
			delimiter = '\n'
		case "Form Feed (FF)":
			delimiter = '\f'
		}
		logger.Debugf("Delimiter is set to: [%+v]", delimiter)
	}
	activity.settings = settings
	return activity, nil
}

// Cleanup ...
func (a *Activity) Cleanup() error {
	logger.Info("Closing connection")
	err := a.connection.Close()
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
	message := input.Data
	if delimiter > 0 {
		logger.Debugf("Appending message delimiter: [%+v]", delimiter)
		message = append(message, delimiter)
	}
	output := &Output{}
	output.BytesWritten, err = a.connection.Write(message)
	if err != nil {
		logger.Errorf("Unable to write the data! %s", err.Error())
		return false, err
	}
	logger.Infof("Written %d bytes", output.BytesWritten)
	if a.settings.WaitForReply {
		output.Data, output.BytesReceived = readData(a.connection)
		logger.Infof("Received %d bytes", output.BytesReceived)
	}
	ctx.SetOutputObject(output)
	return true, nil
}

func readData(conn net.Conn) ([]byte, int) {
	if delimiter != 0 {
		data, err := bufio.NewReader(conn).ReadBytes(delimiter)
		if err != nil {
			errString := err.Error()
			if !strings.Contains(errString, "use of closed network connection") {
				logger.Error("Error reading data from connection: ", err.Error())
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
		errString := err.Error()
		if !strings.Contains(errString, "use of closed network connection") {
			logger.Error("Error reading data from connection: ", err.Error())
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
