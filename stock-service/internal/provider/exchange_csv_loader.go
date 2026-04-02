package provider

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/exbanka/stock-service/internal/model"
)

// LoadExchangesFromCSVFile reads exchanges from a CSV file on disk.
func LoadExchangesFromCSVFile(path string) ([]model.StockExchange, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open csv file: %w", err)
	}
	defer f.Close()
	return parseExchangeCSV(f)
}

func parseExchangeCSV(r io.Reader) ([]model.StockExchange, error) {
	reader := csv.NewReader(r)
	// Skip header
	if _, err := reader.Read(); err != nil {
		return nil, fmt.Errorf("read csv header: %w", err)
	}

	var exchanges []model.StockExchange
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv row: %w", err)
		}
		if len(record) < 10 {
			continue
		}
		exchanges = append(exchanges, model.StockExchange{
			Name:            strings.TrimSpace(record[0]),
			Acronym:         strings.TrimSpace(record[1]),
			MICCode:         strings.TrimSpace(record[2]),
			Polity:          strings.TrimSpace(record[3]),
			Currency:        strings.TrimSpace(record[4]),
			TimeZone:        strings.TrimSpace(record[5]),
			OpenTime:        strings.TrimSpace(record[6]),
			CloseTime:       strings.TrimSpace(record[7]),
			PreMarketOpen:   strings.TrimSpace(record[8]),
			PostMarketClose: strings.TrimSpace(record[9]),
		})
	}
	return exchanges, nil
}
