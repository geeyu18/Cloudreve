package monitor

import (
	"database/sql"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	model "github.com/cloudreve/Cloudreve/v3/models"
	"github.com/cloudreve/Cloudreve/v3/pkg/aria2/common"
	"github.com/cloudreve/Cloudreve/v3/pkg/aria2/rpc"
	"github.com/cloudreve/Cloudreve/v3/pkg/mocks"
	"github.com/cloudreve/Cloudreve/v3/pkg/mq"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	testMock "github.com/stretchr/testify/mock"
	"testing"
)

var mock sqlmock.Sqlmock

// TestMain 初始化数据库Mock
func TestMain(m *testing.M) {
	var db *sql.DB
	var err error
	db, mock, err = sqlmock.New()
	if err != nil {
		panic("An error was not expected when opening a stub database connection")
	}
	model.DB, _ = gorm.Open("mysql", db)
	defer db.Close()
	m.Run()
}

func TestNewMonitor(t *testing.T) {
	a := assert.New(t)
	mockMQ := mq.NewMQ()

	// node not available
	{
		mockPool := &mocks.NodePoolMock{}
		mockPool.On("GetNodeByID", uint(1)).Return(nil)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		task := &model.Download{
			Model: gorm.Model{ID: 1},
		}
		NewMonitor(task, mockPool, mockMQ)
		mockPool.AssertExpectations(t)
		a.NoError(mock.ExpectationsWereMet())
		a.NotEmpty(task.Error)
	}

	// success
	{
		mockNode := &mocks.NodeMock{}
		mockNode.On("GetAria2Instance").Return(&common.DummyAria2{})
		mockPool := &mocks.NodePoolMock{}
		mockPool.On("GetNodeByID", uint(1)).Return(mockNode)

		task := &model.Download{
			Model: gorm.Model{ID: 1},
		}
		NewMonitor(task, mockPool, mockMQ)
		mockNode.AssertExpectations(t)
		mockPool.AssertExpectations(t)
	}

}

func TestMonitor_Loop(t *testing.T) {
	a := assert.New(t)
	mockMQ := mq.NewMQ()
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(&common.DummyAria2{})
	m := &Monitor{
		retried:  MAX_RETRY,
		node:     mockNode,
		Task:     &model.Download{Model: gorm.Model{ID: 1}},
		notifier: mockMQ.Subscribe("test", 1),
	}

	// into interval loop
	{
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		m.Loop(mockMQ)
		a.NoError(mock.ExpectationsWereMet())
		a.NotEmpty(m.Task.Error)
	}

	// into notifier loop
	{
		m.Task.Error = ""
		mockMQ.Publish("test", mq.Message{})
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		m.Loop(mockMQ)
		a.NoError(mock.ExpectationsWereMet())
		a.NotEmpty(m.Task.Error)
	}
}

func TestMonitor_UpdateFailedAfterRetry(t *testing.T) {
	a := assert.New(t)
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(&common.DummyAria2{})
	m := &Monitor{
		node: mockNode,
		Task: &model.Download{Model: gorm.Model{ID: 1}},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	for i := 0; i < MAX_RETRY; i++ {
		a.False(m.Update())
	}

	mockNode.AssertExpectations(t)
	a.True(m.Update())
	a.NoError(mock.ExpectationsWereMet())
	a.NotEmpty(m.Task.Error)
}

func TestMonitor_UpdateMagentoFollow(t *testing.T) {
	a := assert.New(t)
	mockAria2 := &mocks.Aria2Mock{}
	mockAria2.On("Status", testMock.Anything).Return(rpc.StatusInfo{
		FollowedBy: []string{"next"},
	}, nil)
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(mockAria2)
	m := &Monitor{
		node: mockNode,
		Task: &model.Download{Model: gorm.Model{ID: 1}},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	a.False(m.Update())
	a.NoError(mock.ExpectationsWereMet())
	a.Equal("next", m.Task.GID)
	mockAria2.AssertExpectations(t)
}

func TestMonitor_UpdateFailedToUpdateInfo(t *testing.T) {
	a := assert.New(t)
	mockAria2 := &mocks.Aria2Mock{}
	mockAria2.On("Status", testMock.Anything).Return(rpc.StatusInfo{}, nil)
	mockAria2.On("DeleteTempFile", testMock.Anything).Return(nil)
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(mockAria2)
	m := &Monitor{
		node: mockNode,
		Task: &model.Download{Model: gorm.Model{ID: 1}},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnError(errors.New("error"))
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	a.True(m.Update())
	a.NoError(mock.ExpectationsWereMet())
	mockAria2.AssertExpectations(t)
	mockNode.AssertExpectations(t)
	a.NotEmpty(m.Task.Error)
}

func TestMonitor_UpdateCompleted(t *testing.T) {
	a := assert.New(t)
	mockAria2 := &mocks.Aria2Mock{}
	mockAria2.On("Status", testMock.Anything).Return(rpc.StatusInfo{
		Status: "complete",
	}, nil)
	mockAria2.On("DeleteTempFile", testMock.Anything).Return(nil)
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(mockAria2)
	mockNode.On("ID").Return(uint(1))
	m := &Monitor{
		node: mockNode,
		Task: &model.Download{Model: gorm.Model{ID: 1}},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery("SELECT(.+)users(.+)").WillReturnError(errors.New("error"))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	a.True(m.Update())
	a.NoError(mock.ExpectationsWereMet())
	mockAria2.AssertExpectations(t)
	mockNode.AssertExpectations(t)
	a.NotEmpty(m.Task.Error)
}

func TestMonitor_UpdateError(t *testing.T) {
	a := assert.New(t)
	mockAria2 := &mocks.Aria2Mock{}
	mockAria2.On("Status", testMock.Anything).Return(rpc.StatusInfo{
		Status:       "error",
		ErrorMessage: "error",
	}, nil)
	mockAria2.On("DeleteTempFile", testMock.Anything).Return(nil)
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(mockAria2)
	m := &Monitor{
		node: mockNode,
		Task: &model.Download{Model: gorm.Model{ID: 1}},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	a.True(m.Update())
	a.NoError(mock.ExpectationsWereMet())
	mockAria2.AssertExpectations(t)
	mockNode.AssertExpectations(t)
	a.NotEmpty(m.Task.Error)
}

func TestMonitor_UpdateActive(t *testing.T) {
	a := assert.New(t)
	mockAria2 := &mocks.Aria2Mock{}
	mockAria2.On("Status", testMock.Anything).Return(rpc.StatusInfo{
		Status: "active",
	}, nil)
	mockNode := &mocks.NodeMock{}
	mockNode.On("GetAria2Instance").Return(mockAria2)
	m := &Monitor{
		node: mockNode,
		Task: &model.Download{Model: gorm.Model{ID: 1}},
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE(.+)").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	a.False(m.Update())
	a.NoError(mock.ExpectationsWereMet())
	mockAria2.AssertExpectations(t)
	mockNode.AssertExpectations(t)
}
