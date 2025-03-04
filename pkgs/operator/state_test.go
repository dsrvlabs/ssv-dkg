package operator

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"testing"
	"time"

	"github.com/bloxapp/ssv-dkg/pkgs/crypto"
	"github.com/bloxapp/ssv-dkg/pkgs/wire"
	"github.com/bloxapp/ssv/logging"
	"github.com/bloxapp/ssv/utils/rsaencryption"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func singleOperatorKeys(t *testing.T) *rsa.PrivateKey {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return privateKey
}

func generateOperatorsData(t *testing.T, numOps int) (*rsa.PrivateKey, []*wire.Operator) {
	privateKey := singleOperatorKeys(t)
	pkbytes, err := crypto.EncodePublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	ops := make([]*wire.Operator, numOps)

	ops[0] = &wire.Operator{
		ID:     1,
		PubKey: pkbytes,
	}

	for i := 1; i <= numOps-1; i++ {
		priv := singleOperatorKeys(t)
		oppkbytes, err := crypto.EncodePublicKey(&priv.PublicKey)
		require.NoError(t, err)
		ops[i] = &wire.Operator{
			ID:     uint64(i + 1),
			PubKey: oppkbytes,
		}
	}
	return privateKey, ops
}

func TestCreateInstance(t *testing.T) {
	if err := logging.SetGlobalLogger("info", "capital", "console", nil); err != nil {
		panic(err)
	}
	logger := zap.L().Named("state-tests")
	testCreateInstance := func(t *testing.T, numOps int) {
		privateKey, ops := generateOperatorsData(t, numOps)
		s := NewSwitch(privateKey, logger)
		var reqID [24]byte
		copy(reqID[:], "testRequestID1234567890") // Just a sample value
		_, pv, err := rsaencryption.GenerateKeys()
		require.NoError(t, err)
		priv, err := rsaencryption.ConvertPemToPrivateKey(string(pv))
		require.NoError(t, err)
		encPubKey, err := crypto.EncodePublicKey(&priv.PublicKey)
		require.NoError(t, err)

		init := &wire.Init{
			Operators:          ops,
			Owner:              common.HexToAddress("0x0000000"),
			Nonce:              1,
			InitiatorPublicKey: encPubKey,
		}

		inst, resp, err := s.CreateInstance(reqID, init, &priv.PublicKey)

		require.NoError(t, err)
		require.NotNil(t, inst)
		require.NotNil(t, resp)

		wrapper, ok := inst.(*instWrapper)
		require.True(t, ok)
		require.True(t, wrapper.LocalOwner.RSAPub.Equal(&privateKey.PublicKey))
	}

	testParams := []struct {
		ops int
	}{
		{4},
		{7},
		{13},
	}

	for _, param := range testParams {
		t.Run(fmt.Sprintf("Test create instance with %v operators", param.ops), func(t *testing.T) {
			testCreateInstance(t, param.ops)
		})
	}
}

func TestInitInstance(t *testing.T) {
	if err := logging.SetGlobalLogger("info", "capital", "console", nil); err != nil {
		panic(err)
	}
	logger := zap.L().Named("state-tests")
	privateKey, ops := generateOperatorsData(t, 4)
	swtch := NewSwitch(privateKey, logger)
	var reqID [24]byte
	copy(reqID[:], "testRequestID1234567890") // Just a sample value

	_, pv, err := rsaencryption.GenerateKeys()
	require.NoError(t, err)
	priv, err := rsaencryption.ConvertPemToPrivateKey(string(pv))
	require.NoError(t, err)
	encPubKey, err := crypto.EncodePublicKey(&priv.PublicKey)
	require.NoError(t, err)

	init := &wire.Init{
		// Populate the Init message fields as needed for testing
		// For example:
		Operators:          ops,
		Owner:              common.HexToAddress("0x0000000"),
		Nonce:              1,
		InitiatorPublicKey: encPubKey,
	}

	initmsg, err := init.MarshalSSZ()
	require.NoError(t, err)
	initMessage := &wire.Transport{
		Type:       wire.InitMessageType,
		Identifier: reqID,
		Data:       initmsg,
	}
	tsssz, err := initMessage.MarshalSSZ()
	require.NoError(t, err)
	sig, err := crypto.SignRSA(priv, tsssz)
	require.NoError(t, err)
	resp, err := swtch.InitInstance(reqID, initMessage, sig)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Len(t, swtch.Instances, 1)

	resp2, err2 := swtch.InitInstance(reqID, initMessage, sig)
	require.Equal(t, err2, ErrAlreadyExists)
	require.Nil(t, resp2)

	var tested = false

	for i := 0; i < MaxInstances; i++ {
		var reqIDx [24]byte
		copy(reqIDx[:], fmt.Sprintf("testRequestID111111%v1", i)) // Just a sample value
		respx, errx := swtch.InitInstance(reqIDx, initMessage, sig)
		if i == MaxInstances-1 {
			require.Equal(t, errx, ErrMaxInstances)
			require.Nil(t, respx)
			tested = true
			break
		}
		require.NoError(t, errx)
		require.NotNil(t, respx)
	}

	require.True(t, tested)

	swtch.InstanceInitTime[reqID] = time.Now().Add(-6 * time.Minute)

	resp, err = swtch.InitInstance(reqID, initMessage, sig)
	require.NoError(t, err)
	require.NotNil(t, resp)

}

func TestSwitch_cleanInstances(t *testing.T) {
	privateKey, ops := generateOperatorsData(t, 4)
	if err := logging.SetGlobalLogger("info", "capital", "console", nil); err != nil {
		panic(err)
	}
	logger := zap.L().Named("state-tests")
	swtch := NewSwitch(privateKey, logger)
	var reqID [24]byte
	copy(reqID[:], "testRequestID1234567890") // Just a sample value
	_, pv, err := rsaencryption.GenerateKeys()
	require.NoError(t, err)
	priv, err := rsaencryption.ConvertPemToPrivateKey(string(pv))
	require.NoError(t, err)
	encPubKey, err := crypto.EncodePublicKey(&priv.PublicKey)
	require.NoError(t, err)

	init := &wire.Init{
		// Populate the Init message fields as needed for testing
		// For example:
		Operators:          ops,
		Owner:              common.HexToAddress("0x0000000"),
		Nonce:              1,
		InitiatorPublicKey: encPubKey,
	}

	initmsg, err := init.MarshalSSZ()
	require.NoError(t, err)
	initMessage := &wire.Transport{
		Type:       wire.InitMessageType,
		Identifier: reqID,
		Data:       initmsg,
	}
	tsssz, err := initMessage.MarshalSSZ()
	require.NoError(t, err)
	sig, err := crypto.SignRSA(priv, tsssz)
	require.NoError(t, err)
	resp, err := swtch.InitInstance(reqID, initMessage, sig)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, swtch.CleanInstances(), 0)

	require.Len(t, swtch.Instances, 1)
	swtch.InstanceInitTime[reqID] = time.Now().Add(-time.Minute * 6)

	require.Equal(t, swtch.CleanInstances(), 1)
	require.Len(t, swtch.Instances, 0)

}
