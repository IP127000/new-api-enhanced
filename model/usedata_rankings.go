package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type RankingQuotaTotal struct {
	ModelName   string `json:"model_name"`
	TotalTokens int64  `json:"total_tokens"`
}

type RankingQuotaBucket struct {
	ModelName string `json:"model_name"`
	Bucket    int64  `json:"bucket"`
	Tokens    int64  `json:"tokens"`
}

func GetRankingQuotaTotals(startTime int64, endTime int64, userID int) ([]RankingQuotaTotal, error) {
	var rows []RankingQuotaTotal
	tokenExpr := rankingTokenSumExpr()
	query := LOG_DB.Table("logs").
		Select(fmt.Sprintf("model_name, %s as total_tokens", tokenExpr)).
		Where("type = ? and model_name <> ''", LogTypeConsume).
		Group("model_name").
		Having(tokenExpr + " > 0").
		Order("total_tokens DESC, model_name ASC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	query = applyRankingUserScope(query, userID)
	err := query.Find(&rows).Error
	return rows, err
}

func GetRankingQuotaBuckets(startTime int64, endTime int64, bucketSize int64, userID int) ([]RankingQuotaBucket, error) {
	if bucketSize <= 0 {
		bucketSize = 3600
	}
	bucketExpr := rankingBucketExpr(bucketSize)
	tokenExpr := rankingTokenSumExpr()
	var rows []RankingQuotaBucket
	query := LOG_DB.Table("logs").
		Select(fmt.Sprintf("model_name, %s as bucket, %s as tokens", bucketExpr, tokenExpr)).
		Where("type = ? and model_name <> ''", LogTypeConsume).
		Group(fmt.Sprintf("model_name, %s", bucketExpr)).
		Having(tokenExpr + " > 0").
		Order("bucket ASC, model_name ASC")
	query = applyRankingQuotaTimeRange(query, startTime, endTime)
	query = applyRankingUserScope(query, userID)
	err := query.Find(&rows).Error
	return rows, err
}

func rankingTokenSumExpr() string {
	return "COALESCE(SUM(prompt_tokens), 0) + COALESCE(SUM(completion_tokens), 0)"
}

func rankingBucketExpr(bucketSize int64) string {
	if common.UsingMySQL {
		return fmt.Sprintf("FLOOR(created_at / %d) * %d", bucketSize, bucketSize)
	}
	return fmt.Sprintf("(created_at / %d) * %d", bucketSize, bucketSize)
}

func applyRankingQuotaTimeRange(query *gorm.DB, startTime int64, endTime int64) *gorm.DB {
	if startTime > 0 {
		query = query.Where("created_at >= ?", startTime)
	}
	if endTime > 0 {
		query = query.Where("created_at <= ?", endTime)
	}
	return query
}

func applyRankingUserScope(query *gorm.DB, userID int) *gorm.DB {
	if userID > 0 {
		query = query.Where("user_id = ?", userID)
	}
	return query
}
