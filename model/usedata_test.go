package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEarliestQuotaDataTime(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM quota_data").Error)

	require.NoError(t, DB.Create(&QuotaData{
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 2000,
	}).Error)
	require.NoError(t, DB.Create(&QuotaData{
		Username:  "alice",
		ModelName: "gpt-b",
		CreatedAt: 1000,
	}).Error)
	require.NoError(t, DB.Create(&QuotaData{
		Username:  "bob",
		ModelName: "gpt-c",
		CreatedAt: 500,
	}).Error)

	createdAt, err := GetEarliestQuotaDataTime("alice")
	require.NoError(t, err)
	assert.EqualValues(t, 1000, createdAt)

	createdAt, err = GetEarliestQuotaDataTime("")
	require.NoError(t, err)
	assert.EqualValues(t, 500, createdAt)
}
