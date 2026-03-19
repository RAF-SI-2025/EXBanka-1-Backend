package nbs

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRatesXML_ValidInput(t *testing.T) {
	xmlData := []byte(`<KursnaListaModWorker>
        <item>
            <currencyCode>EUR</currencyCode>
            <buyingRate>117.0000</buyingRate>
            <sellingRate>117.5000</sellingRate>
            <middleRate>117.2500</middleRate>
            <unit>1</unit>
        </item>
        <item>
            <currencyCode>JPY</currencyCode>
            <buyingRate>72.0000</buyingRate>
            <sellingRate>73.0000</sellingRate>
            <middleRate>72.5000</middleRate>
            <unit>100</unit>
        </item>
    </KursnaListaModWorker>`)

	rates, err := ParseRatesXML(xmlData)
	require.NoError(t, err)
	assert.Len(t, rates, 2)

	eur := rates["EUR"]
	assert.True(t, eur[0].Equal(decimal.NewFromFloat(117.0)), "EUR buy rate should be 117.0")
	assert.True(t, eur[1].Equal(decimal.NewFromFloat(117.5)), "EUR sell rate should be 117.5")

	jpy := rates["JPY"]
	assert.True(t, jpy[0].Equal(decimal.NewFromFloat(0.72)), "JPY buy rate per unit should be 0.72")
	assert.True(t, jpy[1].Equal(decimal.NewFromFloat(0.73)), "JPY sell rate per unit should be 0.73")
}

func TestParseRatesXML_EmptyInput(t *testing.T) {
	xmlData := []byte(`<KursnaListaModWorker></KursnaListaModWorker>`)
	rates, err := ParseRatesXML(xmlData)
	require.NoError(t, err)
	assert.Empty(t, rates)
}

func TestParseRatesXML_CommaDecimalSeparator(t *testing.T) {
	xmlData := []byte(`<KursnaListaModWorker>
        <item>
            <currencyCode>USD</currencyCode>
            <buyingRate>108,0000</buyingRate>
            <sellingRate>108,5000</sellingRate>
            <middleRate>108,2500</middleRate>
            <unit>1</unit>
        </item>
    </KursnaListaModWorker>`)
	rates, err := ParseRatesXML(xmlData)
	require.NoError(t, err)
	usd := rates["USD"]
	assert.True(t, usd[0].Equal(decimal.NewFromFloat(108.0)))
	assert.True(t, usd[1].Equal(decimal.NewFromFloat(108.5)))
}

func TestParseRatesXML_MalformedRateSkipped(t *testing.T) {
	xmlData := []byte(`<KursnaListaModWorker>
        <item>
            <currencyCode>BAD</currencyCode>
            <buyingRate>not-a-number</buyingRate>
            <sellingRate>0</sellingRate>
            <unit>1</unit>
        </item>
        <item>
            <currencyCode>EUR</currencyCode>
            <buyingRate>117.0</buyingRate>
            <sellingRate>117.5</sellingRate>
            <unit>1</unit>
        </item>
    </KursnaListaModWorker>`)
	rates, err := ParseRatesXML(xmlData)
	require.NoError(t, err)
	assert.Len(t, rates, 1)
	_, hasBAD := rates["BAD"]
	assert.False(t, hasBAD)
}
