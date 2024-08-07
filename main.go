package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/tarm/serial"
)

// Config holds the Modbus configuration
type Config struct {
	ModbusPort          string `json:"modbus_port"`
	ModbusBaud          int    `json:"modbus_baud"`
	ModbusSlaveAddress  byte   `json:"modbus_slave_address"`
	ModbusParity        string `json:"modbus_parity"`
	ModbusStopBit       int    `json:"modbus_stop_bit"`
	ReadIntervalSeconds int    `json:"read_interval_seconds"`
	ReadTimeoutMs       int    `json:"read_timeout_ms"` // Add a read timeout field
}

// Function to calculate CRC-16 for Modbus RTU
func crc16(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if (crc & 0x0001) != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// Function to build and send Modbus RTU request
func sendModbusRequest(port *serial.Port, address byte, functionCode byte, startAddress uint16, numRegisters uint16) ([]byte, error) {
	// Build request frame
	request := []byte{
		address,                   // Slave address
		functionCode,              // Function code
		byte(startAddress >> 8),   // Start address high byte
		byte(startAddress & 0xFF), // Start address low byte
		byte(numRegisters >> 8),   // Number of registers high byte
		byte(numRegisters & 0xFF), // Number of registers low byte
	}

	crc := crc16(request)
	request = append(request, byte(crc&0xFF))
	request = append(request, byte(crc>>8))

	// Send request
	_, err := port.Write(request)
	if err != nil {
		return nil, err
	}

	// Read response
	response := make([]byte, 256)
	n, err := port.Read(response)
	if err != nil {
		return nil, err
	}

	return response[:n], nil
}

// Function to parse the Modbus RTU response
func parseModbusResponse(response []byte, numRegisters int) ([]uint16, error) {
	if len(response) < 3+2*numRegisters {
		return nil, fmt.Errorf("invalid response length")
	}

	// Validate CRC
	crc := crc16(response[:len(response)-2])
	if crc != binary.LittleEndian.Uint16(response[len(response)-2:]) {
		return nil, fmt.Errorf("CRC check failed")
	}

	// Extract data
	data := response[3:] // Skip address and function code
	result := make([]uint16, numRegisters)
	for i := 0; i < numRegisters; i++ {
		result[i] = binary.BigEndian.Uint16(data[i*2 : (i+1)*2])
	}

	return result, nil
}

func main() {
	// Read and parse the configuration file
	configFile, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}
	defer configFile.Close()

	byteValue, err := ioutil.ReadAll(configFile)
	if err != nil {
		log.Fatal(err)
	}

	var config Config
	json.Unmarshal(byteValue, &config)

	// Open serial port
	serialConfig := &serial.Config{
		Name:        config.ModbusPort,
		Baud:        config.ModbusBaud,
		ReadTimeout: time.Millisecond * time.Duration(config.ReadTimeoutMs),
	}

	// Set parity and stop bit
	switch config.ModbusParity {
	case "E":
		serialConfig.Parity = serial.ParityEven
	case "O":
		serialConfig.Parity = serial.ParityOdd
	case "N":
		serialConfig.Parity = serial.ParityNone
	default:
		log.Fatalf("Invalid parity: %s", config.ModbusParity)
	}

	switch config.ModbusStopBit {
	case 1:
		serialConfig.StopBits = serial.Stop1
	case 2:
		serialConfig.StopBits = serial.Stop2
	default:
		log.Fatalf("Invalid stop bit: %d", config.ModbusStopBit)
	}

	port, err := serial.OpenPort(serialConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer port.Close()

	ticker := time.NewTicker(time.Duration(config.ReadIntervalSeconds) * time.Second)
	defer ticker.Stop()

	// Run the periodic read in a separate Goroutine
	go func() {
		for range ticker.C {
			// Read FAN_SPEED from address 4353
			fanSpeedResponse, err := sendModbusRequest(port, config.ModbusSlaveAddress, 0x03, 4353, 1)
			if err != nil {
				log.Printf("Error reading FAN_SPEED: %v", err)
				continue
			}

			fanSpeed, err := parseModbusResponse(fanSpeedResponse, 1)
			if err != nil {
				log.Printf("Error parsing FAN_SPEED response: %v", err)
				continue
			}

			fmt.Printf("FAN_SPEED: %d\n", fanSpeed[0])

			// Read Multisensor_temp from address 4363 (12-bit value)
			tempResponse, err := sendModbusRequest(port, config.ModbusSlaveAddress, 0x03, 4363, 1)
			if err != nil {
				log.Printf("Error reading Multisensor_temp: %v", err)
				continue
			}

			tempData, err := parseModbusResponse(tempResponse, 1)
			if err != nil {
				log.Printf("Error parsing Multisensor_temp response: %v", err)
				continue
			}

			tempValue := tempData[0] & 0x0FFF // Mask to 12 bits
			fmt.Printf("Multisensor_temp: %d\n", tempValue)

			// Read state from address 4609 (0 or 1)
			stateResponse, err := sendModbusRequest(port, config.ModbusSlaveAddress, 0x03, 4609, 1)
			if err != nil {
				log.Printf("Error reading state: %v", err)
				continue
			}

			stateData, err := parseModbusResponse(stateResponse, 1)
			if err != nil {
				log.Printf("Error parsing state response: %v", err)
				continue
			}

			stateValue := stateData[0] & 0x01 // Mask to 1 bit
			state := "home"
			if stateValue == 1 {
				state = "away"
			}

			fmt.Printf("State: %s\n", state)
		}
	}()

	// Keep the main function running
	select {}
}
