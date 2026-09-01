package contracts_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"sort"
	"testing"

	"go.yaml.in/yaml/v3"
)

type legacyTableManifest struct {
	Spec struct {
		ExpectedTableCount int `yaml:"expectedTableCount"`
		Rules              struct {
			TargetMallCompatibilityMutations   string `yaml:"targetMallCompatibilityMutations"`
			TargetTenantCompatibilityMutations string `yaml:"targetTenantCompatibilityMutations"`
			SourceRuntimeProjection            string `yaml:"sourceRuntimeProjection"`
			OwnershipDeployment                string `yaml:"ownershipDeployment"`
		} `yaml:"rules"`
		Tables []struct {
			Name                      string   `yaml:"name"`
			Owner                     string   `yaml:"owner"`
			Domain                    string   `yaml:"domain"`
			TenantScope               string   `yaml:"tenantScope"`
			PrimaryKey                []string `yaml:"primaryKey"`
			Columns                   []string `yaml:"columns"`
			RequiredMigrationEvidence bool     `yaml:"requiredMigrationEvidence"`
		} `yaml:"tables"`
	} `yaml:"spec"`
}

const (
	legacyReceiptSHA256          = "fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76"
	legacyReceiptFileSHA256      = "145d4d34e73741d86161aed086dadc990c7f446777dc4a1c3b5152fab35553c4"
	legacyManifestSHA256         = "c108b11543f41bbd8384540b7314909cd8056e3a141cc7447c443cb98c7e6e5b"
	legacySchemaSHA256           = "afd144a9654602a2452dc20e758b4cc3947a3e0748941ea58da7a53d2874a93a"
	legacyVerifierRevision       = "3eb4c72b485066e7b189446fab5b66a1047e66a2"
	legacyVerifierDigest         = "sha256:a3e1609e75164187557c9207f3565efe7bf8fb413b0adc7f6cceb71c1d531799"
	legacyVerifierJobName        = "mss-shop-legacy-verify-3eb4c72b485066e7b189446fab5b66a1047e66a2"
	legacyVerifierJobUID         = "8721d08a-a4ab-4dfa-9ee2-b69841d1fe3e"
	legacyVerifierPod            = "mss-shop-legacy-verify-3eb4c72b485066e7b189446fab5b66a10475779m"
	legacyVerifierPodUID         = "5ddc28b0-76f3-4799-b1a6-763a549f8696"
	legacyVerificationFileSHA256 = "47878f1f7da8164438604751a89f45775695a1794603296a93d6d5a81499824c"
	legacyVerifierEvidenceSHA256 = "b54d073017cae36f230e1541d2f48692e3744a34a9da252b9a41c6438edc3851"
	annotationFixRevision        = "ebefd1c20bf51f3c43e4a2bb90085fb60ea21442"
	originalDevSafeFieldsSHA256  = "7ddbc7f22749a29a7c019a5fa9f6c5d933cdfdd5fa5cb0e5fb9bc2bab54d8854"
)

type legacyDeliveryRun struct {
	ID                   int64  `yaml:"id"`
	Revision             string `yaml:"revision"`
	Conclusion           string `yaml:"conclusion"`
	Scope                string `yaml:"scope"`
	TenantDigest         string `yaml:"tenantDigest"`
	MallDigest           string `yaml:"mallDigest"`
	ReconcilerDigest     string `yaml:"reconcilerDigest"`
	LegacyImporterDigest string `yaml:"legacyImporterDigest"`
}

type legacyRebuildStatus struct {
	Spec struct {
		DevelopmentIsolation struct {
			OriginalEnvironment        string `yaml:"originalEnvironment"`
			OriginalEnvironmentMutable bool   `yaml:"originalEnvironmentMutable"`
			WriteNamespace             string `yaml:"writeNamespace"`
			InfrastructureObjects      int    `yaml:"infrastructureObjects"`
			FoundationSecrets          int    `yaml:"foundationSecrets"`
			LegacySource               struct {
				Endpoint                 string   `yaml:"endpoint"`
				Access                   string   `yaml:"access"`
				SSLMode                  string   `yaml:"sslMode"`
				FallbackEndpoints        string   `yaml:"fallbackEndpoints"`
				Extensions               []string `yaml:"extensions"`
				ReviewedPublicRoutines   int      `yaml:"reviewedPublicRoutines"`
				UnreviewedPublicRoutines int      `yaml:"unreviewedPublicRoutines"`
				StandalonePublicTypes    int      `yaml:"standalonePublicTypes"`
				RoutineCatalogSHA256     string   `yaml:"routineCatalogSHA256"`
				FingerprintScope         string   `yaml:"fingerprintScope"`
			} `yaml:"legacySource"`
		} `yaml:"developmentIsolation"`
		VerifiedInventory struct {
			LegacyTables                 int `yaml:"legacyTables"`
			TenantPlatformTables         int `yaml:"tenantPlatformTables"`
			MallPlatformTables           int `yaml:"mallPlatformTables"`
			CompatibilityResources       int `yaml:"compatibilityResources"`
			TenantCompatibilityResources int `yaml:"tenantPlatformCompatibilityResources"`
			MallCompatibilityResources   int `yaml:"mallPlatformCompatibilityResources"`
			AdminBusinessViews           int `yaml:"adminBusinessViews"`
			LegacyHTTPRequests           int `yaml:"legacyHTTPRequests"`
			LegacyButtonPermissions      int `yaml:"legacyButtonPermissions"`
			RequiredAcceptanceScenarios  int `yaml:"requiredAcceptanceScenarios"`
		} `yaml:"verifiedInventory"`
		Acceptance struct {
			ClosedScenarios int `yaml:"closedScenarios"`
		} `yaml:"acceptance"`
		EvidenceState struct {
			IndexesConstraintsAndRowCounts     string `yaml:"indexesConstraintsAndRowCounts"`
			CompatibilityOwnershipReassignment string `yaml:"compatibilityOwnershipReassignment"`
			KubernetesSystemAcceptance         string `yaml:"kubernetesSystemAcceptance"`
			ProductionMigration                string `yaml:"productionMigration"`
			DeliveryCI                         string `yaml:"deliveryCI"`
			RemoteDevelopmentPlan              string `yaml:"remoteDevelopmentPlan"`
			IsolatedInfrastructure             string `yaml:"isolatedInfrastructure"`
			FoundationSecrets                  string `yaml:"foundationSecrets"`
			DatastoreReadinessJob              string `yaml:"datastoreReadinessJob"`
			OriginalDevelopmentFingerprint     struct {
				State                    string `yaml:"state"`
				EvidencePath             string `yaml:"evidencePath"`
				EvidenceFileSHA256       string `yaml:"evidenceFileSHA256"`
				SelectedSafeFieldsSHA256 string `yaml:"selectedSafeFieldsSHA256"`
			} `yaml:"originalDevelopmentFingerprint"`
			LegacyImport struct {
				CompiledTables                  int      `yaml:"compiledTables"`
				DataCopyEligibleTables          int      `yaml:"dataCopyEligibleTables"`
				StructureOnlyTables             []string `yaml:"structureOnlyTables"`
				IdentityTablesNotCopied         []string `yaml:"identityTablesNotCopied"`
				AttemptCount                    int      `yaml:"attemptCount"`
				FailedBeforeTargetTransaction   int      `yaml:"failedBeforeTargetTransaction"`
				Receipt                         string   `yaml:"receipt"`
				ReceiptSHA256                   string   `yaml:"receiptSHA256"`
				ReceiptFileSHA256               string   `yaml:"receiptFileSHA256"`
				CurrentMarker                   string   `yaml:"currentMarker"`
				ExpectedSuccessMarkerFormat     string   `yaml:"expectedSuccessMarkerFormat"`
				FailedAttemptEvidence           string   `yaml:"failedAttemptEvidence"`
				FailedAttemptEvidenceFileSHA256 string   `yaml:"failedAttemptEvidenceFileSHA256"`
				SuccessEvidence                 string   `yaml:"successEvidence"`
				SuccessEvidenceFileSHA256       string   `yaml:"successEvidenceFileSHA256"`
				CompleteFailureOutputsCaptured  bool     `yaml:"completeFailureOutputsCaptured"`
				CompleteSuccessReceiptPersisted bool     `yaml:"completeSuccessReceiptPersisted"`
				Attempts                        []struct {
					Revision        string `yaml:"revision"`
					ReadinessResult string `yaml:"readinessResult"`
					ImporterJob     string `yaml:"importerJob"`
					ImageDigest     string `yaml:"imageDigest"`
					Result          string `yaml:"result"`
					FailureStage    string `yaml:"failureStage"`
					ReceiptEmitted  bool   `yaml:"receiptEmitted"`
				} `yaml:"attempts"`
			} `yaml:"legacyImport"`
			PostImportVerifierJob string `yaml:"postImportVerifierJob"`
			PostImportVerifier    struct {
				Revision                           string `yaml:"revision"`
				Job                                string `yaml:"job"`
				JobUID                             string `yaml:"jobUID"`
				Pod                                string `yaml:"pod"`
				PodUID                             string `yaml:"podUID"`
				ImageDigest                        string `yaml:"imageDigest"`
				Result                             string `yaml:"result"`
				Verification                       string `yaml:"verification"`
				VerificationFileSHA256             string `yaml:"verificationFileSHA256"`
				SuccessEvidence                    string `yaml:"successEvidence"`
				SuccessEvidenceFileSHA256          string `yaml:"successEvidenceFileSHA256"`
				AnnotationCompatibilityFixRevision string `yaml:"annotationCompatibilityFixRevision"`
				ReadOnlyExactRetryValidated        bool   `yaml:"readOnlyExactRetryValidated"`
				NewRevisionVerifierJobCreated      bool   `yaml:"newRevisionVerifierJobCreated"`
				NewRevisionVerifierPodCreated      bool   `yaml:"newRevisionVerifierPodCreated"`
				ReceiptConfigMapChangedByFix       bool   `yaml:"receiptConfigMapChangedByFix"`
				KubernetesWritesFromFixValidation  bool   `yaml:"kubernetesWritesFromFixValidation"`
			} `yaml:"postImportVerifier"`
			ReconciliationSecretsOperator  string `yaml:"reconciliationSecretsOperator"`
			ReconcilerJob                  string `yaml:"reconcilerJob"`
			IsolatedInAppBrowserAcceptance string `yaml:"isolatedInAppBrowserAcceptance"`
		} `yaml:"evidenceState"`
		LocalEvidence struct {
			MallAdminWeb struct {
				ResourceRoutes      int    `yaml:"resourceRoutes"`
				OwnershipDeployment string `yaml:"ownershipDeployment"`
			} `yaml:"mallAdminWeb"`
			TenantAdminWeb struct {
				ResourceRoutes      int    `yaml:"resourceRoutes"`
				TargetResource      string `yaml:"targetResource"`
				OwnershipDeployment string `yaml:"ownershipDeployment"`
			} `yaml:"tenantAdminWeb"`
			CurrentSourceValidation struct {
				IsolatedCreateOnlyResources struct {
					InfrastructureObjects     int `yaml:"infrastructureObjects"`
					FoundationSecrets         int `yaml:"foundationSecrets"`
					SuccessfulReadinessJobs   int `yaml:"successfulReadinessJobs"`
					FailedImporterJobs        int `yaml:"failedImporterJobs"`
					SuccessfulImporterJobs    int `yaml:"successfulImporterJobs"`
					ReceiptEvidenceConfigMaps int `yaml:"receiptEvidenceConfigMaps"`
					SuccessfulVerifierJobs    int `yaml:"successfulVerifierJobs"`
					Total                     int `yaml:"total"`
				} `yaml:"isolatedCreateOnlyResources"`
				OriginalDevelopmentReady                    bool   `yaml:"originalDevelopmentReady"`
				OriginalDevelopmentSelectedSafeFieldsSHA256 string `yaml:"originalDevelopmentSelectedSafeFieldsSHA256"`
				IsolatedNamespaceExists                     bool   `yaml:"isolatedNamespaceExists"`
				IsolatedPostgreSQLReady                     bool   `yaml:"isolatedPostgreSQLReady"`
				IsolatedRedisReady                          bool   `yaml:"isolatedRedisReady"`
				SuccessfulLegacyImport                      bool   `yaml:"successfulLegacyImport"`
				SuccessfulPostImportVerifier                bool   `yaml:"successfulPostImportVerifier"`
			} `yaml:"currentSourceValidation"`
			DeliveryImages struct {
				CurrentFourImagePublication string            `yaml:"currentFourImagePublication"`
				VerifiedRun                 legacyDeliveryRun `yaml:"verifiedRun"`
				VerifierRun                 legacyDeliveryRun `yaml:"verifierRun"`
			} `yaml:"deliveryImages"`
		} `yaml:"localEvidence"`
		SourcesOfTruth map[string]string `yaml:"sourcesOfTruth"`
		Safety         struct {
			ProductionReadOnly                      bool   `yaml:"productionReadOnly"`
			OriginalDevelopmentEnvironmentImmutable bool   `yaml:"originalDevelopmentEnvironmentImmutable"`
			IsolatedDevelopmentWriteNamespace       string `yaml:"isolatedDevelopmentWriteNamespace"`
			OldDevelopmentWritesForbidden           bool   `yaml:"oldDevelopmentWritesForbidden"`
			ProductionOrderRowsToDevelopment        bool   `yaml:"productionOrderRowsToDevelopment"`
			TargetOrderRowsImported                 bool   `yaml:"targetOrderRowsImported"`
			TargetOrderGoodsRowsImported            bool   `yaml:"targetOrderGoodsRowsImported"`
			SystemTestsUseDisposableKubernetesPods  bool   `yaml:"systemTestsUseDisposableKubernetesPods"`
			IsolatedInfrastructureStaged            bool   `yaml:"isolatedInfrastructureStaged"`
			IsolatedAdminRuntimeDeployed            bool   `yaml:"isolatedAdminRuntimeDeployed"`
			LegacyImportAttempts                    int    `yaml:"legacyImportAttempts"`
			LegacyImportSucceeded                   bool   `yaml:"legacyImportSucceeded"`
			PostImportVerifierSucceeded             bool   `yaml:"postImportVerifierSucceeded"`
			IsolatedBrowserAcceptanceExecuted       bool   `yaml:"isolatedBrowserAcceptanceExecuted"`
		} `yaml:"safety"`
	} `yaml:"spec"`
}

type legacyVerificationDocument struct {
	Version         string `json:"version"`
	TargetDatabase  string `json:"targetDatabase"`
	DatabaseMarker  string `json:"databaseMarker"`
	ReceiptSHA256   string `json:"receiptSHA256"`
	ManifestSHA256  string `json:"manifestSHA256"`
	SchemaSHA256    string `json:"schemaSHA256"`
	TableCount      int    `json:"tableCount"`
	OrdersRows      int64  `json:"ordersRows"`
	OrderGoodsRows  int64  `json:"orderGoodsRows"`
	Namespace       string `json:"namespace"`
	PodName         string `json:"podName"`
	PodUID          string `json:"podUID"`
	Revision        string `json:"revision"`
	ImageRepository string `json:"imageRepository"`
	ImageDigest     string `json:"imageDigest"`
	ImageReference  string `json:"imageReference"`
}

type legacyVerifierSuccessEvidence struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Namespace  string `yaml:"namespace"`
		CapturedAt string `yaml:"capturedAt"`
	} `yaml:"metadata"`
	Spec struct {
		Receipt struct {
			CanonicalPayloadSHA256 string `yaml:"canonicalPayloadSHA256"`
			FileSHA256             string `yaml:"fileSHA256"`
			ManifestSHA256         string `yaml:"manifestSHA256"`
			SchemaSHA256           string `yaml:"schemaSHA256"`
			DatabaseMarker         string `yaml:"databaseMarker"`
		} `yaml:"receipt"`
		ImmutableEvidenceConfigMap struct {
			Name              string   `yaml:"name"`
			UID               string   `yaml:"uid"`
			CreatedAt         string   `yaml:"createdAt"`
			Revision          string   `yaml:"revision"`
			Immutable         bool     `yaml:"immutable"`
			DataKeys          []string `yaml:"dataKeys"`
			ReceiptFileSHA256 string   `yaml:"receiptFileSHA256"`
			SafeBindingSHA256 string   `yaml:"safeBindingSHA256"`
		} `yaml:"immutableEvidenceConfigMap"`
		Verifier struct {
			Revision         string `yaml:"revision"`
			Job              string `yaml:"job"`
			JobUID           string `yaml:"jobUID"`
			JobCreatedAt     string `yaml:"jobCreatedAt"`
			JobCompletedAt   string `yaml:"jobCompletedAt"`
			JobSucceeded     int    `yaml:"jobSucceeded"`
			JobFailed        int    `yaml:"jobFailed"`
			Pod              string `yaml:"pod"`
			PodUID           string `yaml:"podUID"`
			PodPhase         string `yaml:"podPhase"`
			RestartCount     int    `yaml:"restartCount"`
			Retries          int    `yaml:"retries"`
			ImageRepository  string `yaml:"imageRepository"`
			ImageDigest      string `yaml:"imageDigest"`
			ImageReference   string `yaml:"imageReference"`
			RuntimeImageID   string `yaml:"runtimeImageID"`
			OutputPath       string `yaml:"outputPath"`
			OutputFileSHA256 string `yaml:"outputFileSHA256"`
			JobBindingSHA256 string `yaml:"jobBindingSHA256"`
			PodBindingSHA256 string `yaml:"podBindingSHA256"`
			TargetDatabase   string `yaml:"targetDatabase"`
			TableCount       int    `yaml:"tableCount"`
			OrdersRows       int64  `yaml:"ordersRows"`
			OrderGoodsRows   int64  `yaml:"orderGoodsRows"`
			Result           string `yaml:"result"`
		} `yaml:"verifier"`
		NetworkBoundary struct {
			Policy                               string `yaml:"policy"`
			PolicyUID                            string `yaml:"policyUID"`
			SafeBindingSHA256                    string `yaml:"safeBindingSHA256"`
			Target                               string `yaml:"target"`
			LegacySourceCredentialMounted        bool   `yaml:"legacySourceCredentialMounted"`
			LegacySourceEndpointPresent          bool   `yaml:"legacySourceEndpointPresent"`
			RedisCredentialMounted               bool   `yaml:"redisCredentialMounted"`
			KubernetesServiceAccountTokenMounted bool   `yaml:"kubernetesServiceAccountTokenMounted"`
		} `yaml:"networkBoundary"`
		StageHistory struct {
			InitialCreate struct {
				OperatorRevision                 string `yaml:"operatorRevision"`
				Result                           string `yaml:"result"`
				FailureReason                    string `yaml:"failureReason"`
				ConfigMapCreated                 bool   `yaml:"configMapCreated"`
				VerifierJobCreated               bool   `yaml:"verifierJobCreated"`
				VerifierOutputAcceptedAtThatTime bool   `yaml:"verifierOutputAcceptedAtThatTime"`
				ResourcesPreserved               bool   `yaml:"resourcesPreserved"`
			} `yaml:"initialCreate"`
			AnnotationCompatibilityFix struct {
				Revision                  string `yaml:"revision"`
				ExactKubeSphereVersion    string `yaml:"exactKubeSphereVersion"`
				SourceTestsPassed         bool   `yaml:"sourceTestsPassed"`
				IndependentReviewBlockers int    `yaml:"independentReviewBlockers"`
			} `yaml:"annotationCompatibilityFix"`
			RejectedNewRevisionDryRun struct {
				TargetRevision            string `yaml:"targetRevision"`
				Result                    string `yaml:"result"`
				Reason                    string `yaml:"reason"`
				KubernetesWritesPerformed bool   `yaml:"kubernetesWritesPerformed"`
				ReceiptConfigMapChanged   bool   `yaml:"receiptConfigMapChanged"`
				NewVerifierJobCreated     bool   `yaml:"newVerifierJobCreated"`
				NewVerifierPodCreated     bool   `yaml:"newVerifierPodCreated"`
			} `yaml:"rejectedNewRevisionDryRun"`
			ReadOnlyExactRetryAudit struct {
				OperatorRevision          string `yaml:"operatorRevision"`
				TargetRevision            string `yaml:"targetRevision"`
				OperatorBinarySHA256      string `yaml:"operatorBinarySHA256"`
				StageLogSHA256            string `yaml:"stageLogSHA256"`
				CreateFlag                bool   `yaml:"createFlag"`
				Created                   bool   `yaml:"created"`
				ExactRetry                bool   `yaml:"exactRetry"`
				DryRun                    bool   `yaml:"dryRun"`
				KubernetesWritesPerformed bool   `yaml:"kubernetesWritesPerformed"`
				Result                    string `yaml:"result"`
			} `yaml:"readOnlyExactRetryAudit"`
		} `yaml:"stageHistory"`
		DeliveryCI struct {
			VerifierWorkload struct {
				RunID                int64  `yaml:"runID"`
				Revision             string `yaml:"revision"`
				Conclusion           string `yaml:"conclusion"`
				TenantDigest         string `yaml:"tenantDigest"`
				MallDigest           string `yaml:"mallDigest"`
				ReconcilerDigest     string `yaml:"reconcilerDigest"`
				LegacyImporterDigest string `yaml:"legacyImporterDigest"`
			} `yaml:"verifierWorkload"`
			AnnotationCompatibilityFix struct {
				RunID                int64  `yaml:"runID"`
				Revision             string `yaml:"revision"`
				Conclusion           string `yaml:"conclusion"`
				Deployed             bool   `yaml:"deployed"`
				TenantDigest         string `yaml:"tenantDigest"`
				MallDigest           string `yaml:"mallDigest"`
				ReconcilerDigest     string `yaml:"reconcilerDigest"`
				LegacyImporterDigest string `yaml:"legacyImporterDigest"`
			} `yaml:"annotationCompatibilityFix"`
		} `yaml:"deliveryCI"`
		OriginalDevelopmentBoundary struct {
			Environment                  string `yaml:"environment"`
			EvidencePath                 string `yaml:"evidencePath"`
			EvidenceFileSHA256           string `yaml:"evidenceFileSHA256"`
			SelectedSafeFieldsSHA256     string `yaml:"selectedSafeFieldsSHA256"`
			UnchangedFromImportBoundary  bool   `yaml:"unchangedFromImportBoundary"`
			SecretsAccessed              bool   `yaml:"secretsAccessed"`
			DatabaseConnectionsPerformed bool   `yaml:"databaseConnectionsPerformed"`
			WritesPerformed              bool   `yaml:"writesPerformed"`
		} `yaml:"originalDevelopmentBoundary"`
		CurrentState struct {
			PostImportVerifierExecuted              bool `yaml:"postImportVerifierExecuted"`
			ReconciliationExecuted                  bool `yaml:"reconciliationExecuted"`
			AdminRuntimeDeployed                    bool `yaml:"adminRuntimeDeployed"`
			IsolatedBrowserAcceptanceExecuted       bool `yaml:"isolatedBrowserAcceptanceExecuted"`
			BusinessAcceptanceScenariosClosed       int  `yaml:"businessAcceptanceScenariosClosed"`
			BusinessAcceptanceScenariosRequired     int  `yaml:"businessAcceptanceScenariosRequired"`
			OriginalDevelopmentEnvironmentUnchanged bool `yaml:"originalDevelopmentEnvironmentUnchanged"`
			ProductionResourcesAccessed             bool `yaml:"productionResourcesAccessed"`
		} `yaml:"currentState"`
	} `yaml:"spec"`
}

func decodeExactLegacyVerification(data []byte) (legacyVerificationDocument, error) {
	wantFields := []string{
		"databaseMarker", "imageDigest", "imageReference", "imageRepository",
		"manifestSHA256", "namespace", "orderGoodsRows", "ordersRows", "podName",
		"podUID", "receiptSHA256", "revision", "schemaSHA256", "tableCount",
		"targetDatabase", "version",
	}
	fieldDecoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := fieldDecoder.Token()
	if err != nil || opening != json.Delim('{') {
		return legacyVerificationDocument{}, errors.New("verification evidence is not one JSON object")
	}
	seen := make(map[string]struct{}, len(wantFields))
	for fieldDecoder.More() {
		token, err := fieldDecoder.Token()
		if err != nil {
			return legacyVerificationDocument{}, errors.New("read verification evidence field")
		}
		name, ok := token.(string)
		if !ok {
			return legacyVerificationDocument{}, errors.New("verification evidence field name is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return legacyVerificationDocument{}, errors.New("verification evidence contains a duplicate field")
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := fieldDecoder.Decode(&value); err != nil {
			return legacyVerificationDocument{}, errors.New("read verification evidence value")
		}
	}
	closing, err := fieldDecoder.Token()
	if err != nil || closing != json.Delim('}') {
		return legacyVerificationDocument{}, errors.New("verification evidence object is incomplete")
	}
	var fieldTrailing any
	if err := fieldDecoder.Decode(&fieldTrailing); !errors.Is(err, io.EOF) {
		return legacyVerificationDocument{}, errors.New("verification evidence has trailing JSON")
	}
	gotFields := make([]string, 0, len(seen))
	for name := range seen {
		gotFields = append(gotFields, name)
	}
	sort.Strings(gotFields)
	if !reflect.DeepEqual(gotFields, wantFields) {
		return legacyVerificationDocument{}, errors.New("verification evidence fields differ from the reviewed 16-field schema")
	}

	typedDecoder := json.NewDecoder(bytes.NewReader(data))
	typedDecoder.DisallowUnknownFields()
	var result legacyVerificationDocument
	if err := typedDecoder.Decode(&result); err != nil {
		return legacyVerificationDocument{}, errors.New("decode verification evidence")
	}
	var typedTrailing any
	if err := typedDecoder.Decode(&typedTrailing); !errors.Is(err, io.EOF) {
		return legacyVerificationDocument{}, errors.New("verification evidence has trailing JSON")
	}
	return result, nil
}

func TestLegacyTableManifestIsTheCompleteCompatibilityBoundary(t *testing.T) {
	content, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest: %v", err)
	}
	var manifest legacyTableManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse legacy table manifest: %v", err)
	}
	if manifest.Spec.ExpectedTableCount != 54 {
		t.Fatalf("expected table count = %d, want 54", manifest.Spec.ExpectedTableCount)
	}
	if len(manifest.Spec.Tables) != manifest.Spec.ExpectedTableCount {
		t.Fatalf("manifest tables = %d, want %d", len(manifest.Spec.Tables), manifest.Spec.ExpectedTableCount)
	}

	names := make([]string, 0, len(manifest.Spec.Tables))
	seen := make(map[string]struct{}, len(manifest.Spec.Tables))
	ownerCounts := map[string]int{}
	shippingWarehouseCovered := false
	for _, table := range manifest.Spec.Tables {
		if table.Name == "" || len(table.PrimaryKey) == 0 || len(table.Columns) == 0 {
			t.Fatalf("incomplete legacy table contract: %#v", table)
		}
		if _, duplicate := seen[table.Name]; duplicate {
			t.Fatalf("duplicate legacy table %q", table.Name)
		}
		seen[table.Name] = struct{}{}
		names = append(names, table.Name)
		ownerCounts[table.Owner]++

		columns := make(map[string]struct{}, len(table.Columns))
		for _, column := range table.Columns {
			if _, duplicate := columns[column]; duplicate {
				t.Fatalf("table %q has duplicate column %q", table.Name, column)
			}
			columns[column] = struct{}{}
		}
		for _, key := range table.PrimaryKey {
			if _, exists := columns[key]; !exists {
				t.Fatalf("table %q primary key %q is not a declared column", table.Name, key)
			}
		}
		if table.Name == "shipping_warehouses" {
			shippingWarehouseCovered = table.RequiredMigrationEvidence && len(table.Columns) == 16
		}
	}

	if _, exists := seen["area"]; exists {
		t.Fatal("area is a tool-owned model, not one of the 54 business tables")
	}
	if _, exists := seen["order_operate_logs"]; exists {
		t.Fatal("order_operate_logs has no migrated legacy table contract")
	}
	if !shippingWarehouseCovered {
		t.Fatal("shipping_warehouses must carry explicit migration evidence and all 16 columns")
	}
	if ownerCounts["tenant-platform"] != 4 || ownerCounts["mall-platform"] != 50 {
		t.Fatalf("owner split = %#v, want tenant-platform=4 mall-platform=50", ownerCounts)
	}
	rules := manifest.Spec.Rules
	if rules.TargetMallCompatibilityMutations != "read-only-50-resources" ||
		rules.TargetTenantCompatibilityMutations != "read-only-1-payment-resource" ||
		rules.SourceRuntimeProjection != "read-only-50-mall-1-tenant-payment" ||
		rules.OwnershipDeployment != "pending-forward-migrations-not-applied" {
		t.Fatalf("legacy compatibility safety rules drifted: %#v", rules)
	}

	wantTenantOwned := map[string]string{
		"brands":             "catalog_master",
		"categories":         "catalog_master",
		"classes":            "catalog_master",
		"goods_infos":        "catalog_master",
		"couriers":           "logistics_rule",
		"courier_pack_rules": "logistics_rule",
		"courier_links":      "logistics_rule",
	}
	for _, table := range manifest.Spec.Tables {
		domain, moved := wantTenantOwned[table.Name]
		if !moved {
			continue
		}
		if table.Owner != "mall-platform" || table.Domain != domain || table.TenantScope != "tenant" {
			t.Errorf("tenant-owned product/logistics contract for %q = owner %q domain %q scope %q", table.Name, table.Owner, table.Domain, table.TenantScope)
		}
		delete(wantTenantOwned, table.Name)
	}
	if len(wantTenantOwned) != 0 {
		t.Fatalf("tenant-owned product/logistics tables missing from manifest: %#v", wantTenantOwned)
	}

	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for index := range names {
		if names[index] != sorted[index] {
			t.Fatalf("legacy tables are not sorted at index %d: %q before %q", index, names[index], sorted[index])
		}
	}
}

func TestLegacyRebuildMemoryStaysAlignedWithExecutableContracts(t *testing.T) {
	content, err := os.ReadFile("../docs/project/legacy-rebuild-status.yaml")
	if err != nil {
		t.Fatalf("read legacy rebuild status: %v", err)
	}
	var status legacyRebuildStatus
	if err := yaml.Unmarshal(content, &status); err != nil {
		t.Fatalf("parse legacy rebuild status: %v", err)
	}

	isolation := status.Spec.DevelopmentIsolation
	if isolation.OriginalEnvironment != "r1shop-dev" || isolation.OriginalEnvironmentMutable ||
		isolation.WriteNamespace != "mss-shop-dev" || isolation.InfrastructureObjects != 24 ||
		isolation.FoundationSecrets != 6 {
		t.Fatalf("development isolation boundary drifted: %#v", isolation)
	}
	legacySource := isolation.LegacySource
	if legacySource.Endpoint != "timescaledb-r1shop-dev.database.svc:5432/r1shop_dev" ||
		legacySource.Access != "read-only-repeatable-read" ||
		legacySource.SSLMode != "disabled-fixed-source-exception" ||
		legacySource.FallbackEndpoints != "forbidden" ||
		!reflect.DeepEqual(legacySource.Extensions, []string{
			"plpgsql|1.0|pg_catalog",
			"timescaledb|2.20.2|public",
		}) || legacySource.ReviewedPublicRoutines != 91 ||
		legacySource.UnreviewedPublicRoutines != 0 || legacySource.StandalonePublicTypes != 0 ||
		legacySource.RoutineCatalogSHA256 != "32c0b88f3178e4a15647eef85da4a718b4e490070bd7fa2c77876101f386d81e" ||
		legacySource.FingerprintScope != "current-source-instance-complete-pg-proc-rows-ordered-by-oid" {
		t.Fatalf("reviewed legacy source catalog drifted: %#v", legacySource)
	}
	importerCatalogSource, err := os.ReadFile("../services/legacy-importer/internal/importer/catalog.go")
	if err != nil {
		t.Fatalf("read legacy importer catalog contract: %v", err)
	}
	if !regexp.MustCompile(`(?m)expectedSourceRoutineCount\s*=\s*91`).Match(importerCatalogSource) ||
		!regexp.MustCompile(`(?m)expectedSourceRoutineSHA256\s*=\s*"32c0b88f3178e4a15647eef85da4a718b4e490070bd7fa2c77876101f386d81e"`).Match(importerCatalogSource) {
		t.Fatal("legacy source memory no longer matches the executable importer routine contract")
	}
	importerRunSource, err := os.ReadFile("../services/legacy-importer/internal/importer/run.go")
	if err != nil {
		t.Fatalf("read legacy importer source extension contract: %v", err)
	}
	for _, extension := range legacySource.Extensions {
		if !regexp.MustCompile(regexp.QuoteMeta(`"` + extension + `"`)).Match(importerRunSource) {
			t.Fatalf("legacy source extension %q is missing from the executable importer contract", extension)
		}
	}

	inventory := status.Spec.VerifiedInventory
	if inventory.LegacyTables != 54 || inventory.TenantPlatformTables != 4 || inventory.MallPlatformTables != 50 {
		t.Fatalf("legacy owner inventory drifted: %#v", inventory)
	}
	if inventory.CompatibilityResources != 51 || inventory.TenantCompatibilityResources != 1 || inventory.MallCompatibilityResources != 50 {
		t.Fatalf("legacy compatibility allocation drifted: %#v", inventory)
	}
	if status.Spec.EvidenceState.CompatibilityOwnershipReassignment != "implemented-source-not-deployed" {
		t.Fatalf("ownership reassignment state = %q, want source-only implementation", status.Spec.EvidenceState.CompatibilityOwnershipReassignment)
	}
	evidence := status.Spec.EvidenceState
	if evidence.IndexesConstraintsAndRowCounts != "import-and-independent-post-import-verifier-passed" ||
		evidence.KubernetesSystemAcceptance != "partial-datastore-import-and-verifier-only" ||
		evidence.ProductionMigration != "forbidden-without-explicit-approval" ||
		evidence.DeliveryCI != "four-image-publications-verified-through-revision-c-evidence" ||
		evidence.RemoteDevelopmentPlan != "development-first-release-candidate-validation-authorized-revision-c-evidence-current" ||
		evidence.IsolatedInfrastructure != "created-exact-24-objects" ||
		evidence.FoundationSecrets != "created-exact-six-immutable" ||
		evidence.DatastoreReadinessJob != "passed-revision-6fed45f" ||
		evidence.PostImportVerifierJob != "passed-revision-b-3eb4c72" ||
		evidence.ReconciliationSecretsOperator != "implemented-default-server-dry-run-explicit-create-not-executed" ||
		evidence.ReconcilerJob != "source-implemented-not-executed" ||
		evidence.IsolatedInAppBrowserAcceptance != "pending-not-deployed" {
		t.Fatalf("isolated infrastructure evidence drifted: %#v", evidence)
	}
	fingerprint := evidence.OriginalDevelopmentFingerprint
	if fingerprint.State != "selected-safe-fields-unchanged-through-post-verifier-revision-ebefd1c" ||
		fingerprint.EvidencePath != "docs/evidence/original-dev/2026-09-01-post-verifier-ebefd1c.json" ||
		fingerprint.EvidenceFileSHA256 != "07eafdd704a05b1768f026ee00a33f53134cd8eba3eadd2e3b6c8ba712448f75" ||
		fingerprint.SelectedSafeFieldsSHA256 != originalDevSafeFieldsSHA256 {
		t.Fatalf("original development fingerprint evidence drifted: %#v", fingerprint)
	}
	fingerprintContent, err := os.ReadFile("../" + fingerprint.EvidencePath)
	if err != nil {
		t.Fatalf("read original development fingerprint evidence: %v", err)
	}
	fingerprintFileDigest := sha256.Sum256(fingerprintContent)
	if hex.EncodeToString(fingerprintFileDigest[:]) != fingerprint.EvidenceFileSHA256 {
		t.Fatal("original development fingerprint evidence file digest drifted")
	}
	var fingerprintEvidence struct {
		Version                  string          `json:"version"`
		Revision                 string          `json:"revision"`
		Environment              string          `json:"environment"`
		AccessMode               string          `json:"accessMode"`
		SelectedSafeFields       json.RawMessage `json:"selectedSafeFields"`
		SelectedSafeFieldsSHA256 string          `json:"selectedSafeFieldsSHA256"`
		SecretsAccessed          bool            `json:"secretsAccessed"`
		DatabaseConnections      bool            `json:"databaseConnectionsPerformed"`
		WritesPerformed          bool            `json:"writesPerformed"`
	}
	fingerprintDecoder := json.NewDecoder(bytes.NewReader(fingerprintContent))
	fingerprintDecoder.DisallowUnknownFields()
	if err := fingerprintDecoder.Decode(&fingerprintEvidence); err != nil {
		t.Fatalf("parse original development fingerprint evidence: %v", err)
	}
	var fingerprintTrailing any
	if err := fingerprintDecoder.Decode(&fingerprintTrailing); !errors.Is(err, io.EOF) {
		t.Fatal("original development fingerprint evidence has trailing JSON")
	}
	if fingerprintEvidence.Version != "mss-shop-original-dev-read-only-kubernetes-fingerprint/v1" ||
		len(fingerprintEvidence.SelectedSafeFields) == 0 ||
		fingerprintEvidence.Revision != annotationFixRevision ||
		fingerprintEvidence.Environment != "r1shop-dev-read-only" ||
		fingerprintEvidence.AccessMode != "kubernetes-fixed-get-list-only" ||
		fingerprintEvidence.SelectedSafeFieldsSHA256 != fingerprint.SelectedSafeFieldsSHA256 ||
		fingerprintEvidence.SecretsAccessed || fingerprintEvidence.DatabaseConnections ||
		fingerprintEvidence.WritesPerformed {
		t.Fatalf("unsafe original development fingerprint evidence: %#v", fingerprintEvidence)
	}
	legacyImport := evidence.LegacyImport
	if legacyImport.CompiledTables != 51 || legacyImport.DataCopyEligibleTables != 49 ||
		!reflect.DeepEqual(legacyImport.StructureOnlyTables, []string{"orders", "order_goods"}) ||
		!reflect.DeepEqual(legacyImport.IdentityTablesNotCopied, []string{"roles", "tenants", "users"}) ||
		legacyImport.AttemptCount != 3 || legacyImport.FailedBeforeTargetTransaction != 2 ||
		legacyImport.Receipt != "docs/evidence/legacy-import/fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76/receipt.json" ||
		legacyImport.ReceiptSHA256 != "fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76" ||
		legacyImport.ReceiptFileSHA256 != "145d4d34e73741d86161aed086dadc990c7f446777dc4a1c3b5152fab35553c4" ||
		legacyImport.CurrentMarker != "mss-shop-isolated-dev:legacy-import:v1:fa666688d8df975344030f31266072605031da1cd22cfcc341326f909071ef76" ||
		legacyImport.ExpectedSuccessMarkerFormat != "mss-shop-isolated-dev:legacy-import:v1:<receipt-sha256>" ||
		legacyImport.FailedAttemptEvidence != "docs/evidence/mss-shop-dev/2026-09-01-import-attempts.yaml" ||
		legacyImport.FailedAttemptEvidenceFileSHA256 != "25918a8463d68385f14d211f38f5dfc746e6949a2b439ee1e2da5c10745ef634" ||
		legacyImport.SuccessEvidence != "docs/evidence/mss-shop-dev/2026-09-01-import-success.yaml" ||
		legacyImport.SuccessEvidenceFileSHA256 != "25099872e65d4701ef08bc33b7c65b2471325c71ff6499ecd242bba3d585e48e" ||
		!legacyImport.CompleteFailureOutputsCaptured || !legacyImport.CompleteSuccessReceiptPersisted ||
		len(legacyImport.Attempts) != 3 {
		t.Fatalf("legacy import evidence drifted: %#v", legacyImport)
	}
	wantAttemptRevisions := []string{
		"a60ef82bf83f0661809aa87340178b3e58dff48d",
		"12c6a682e38bfef165e09d108e0bd77c53ee73ca",
	}
	wantAttemptDigests := []string{
		"sha256:532335acc7f8804be27d1fcd069704bb8a626523f93511d0c4bfd1bdf469dd01",
		"sha256:9eb9efcb01ff5ac115b6df772f68139cba9ce53f691a310756f81cceb161fb05",
	}
	for index, attempt := range legacyImport.Attempts[:2] {
		if attempt.Revision != wantAttemptRevisions[index] || attempt.ImageDigest != wantAttemptDigests[index] ||
			attempt.ReadinessResult != "passed" || attempt.ImporterJob == "" ||
			attempt.Result != "failed" || attempt.FailureStage == "" || attempt.ReceiptEmitted {
			t.Fatalf("legacy import attempt %d drifted: %#v", index+1, attempt)
		}
	}
	successfulAttempt := legacyImport.Attempts[2]
	if successfulAttempt.Revision != "6fed45f354e93efe104045c6dde86ac33c368d6d" ||
		successfulAttempt.ReadinessResult != "passed" ||
		successfulAttempt.ImporterJob != "mss-shop-legacy-import-6fed45f354e93efe104045c6dde86ac33c368d6d" ||
		successfulAttempt.ImageDigest != "sha256:881f105ea00dfac3bf4381e0177ad1349998d51059beeb155e1a96c64bbe3ba3" ||
		successfulAttempt.Result != "succeeded" || successfulAttempt.FailureStage != "" ||
		!successfulAttempt.ReceiptEmitted {
		t.Fatalf("successful legacy import attempt drifted: %#v", successfulAttempt)
	}
	importEvidenceContent, err := os.ReadFile("../" + legacyImport.FailedAttemptEvidence)
	if err != nil {
		t.Fatalf("read isolated import attempt evidence: %v", err)
	}
	importEvidenceFileDigest := sha256.Sum256(importEvidenceContent)
	if hex.EncodeToString(importEvidenceFileDigest[:]) != legacyImport.FailedAttemptEvidenceFileSHA256 {
		t.Fatal("isolated import attempt evidence file digest drifted")
	}
	var importEvidence struct {
		Metadata struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			TargetDatabase string `yaml:"targetDatabase"`
			InitialMarker  string `yaml:"initialMarker"`
			Attempts       []struct {
				Revision    string `yaml:"revision"`
				ImageDigest string `yaml:"imageDigest"`
				Readiness   struct {
					Result string `yaml:"result"`
				} `yaml:"readiness"`
				Importer struct {
					FailureStage             string `yaml:"failureStage"`
					CompleteOutput           string `yaml:"completeOutput"`
					CompleteOutputSHA256     string `yaml:"completeOutputSHA256"`
					TargetTransactionStarted bool   `yaml:"targetTransactionStarted"`
					ReceiptEmitted           bool   `yaml:"receiptEmitted"`
				} `yaml:"importer"`
			} `yaml:"attempts"`
			CurrentState struct {
				SuccessfulImport         bool `yaml:"successfulImport"`
				TargetWritesFromAttempts bool `yaml:"targetWritesFromAttempts"`
				ReceiptEmitted           bool `yaml:"receiptEmitted"`
			} `yaml:"currentState"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(importEvidenceContent, &importEvidence); err != nil {
		t.Fatalf("parse isolated import attempt evidence: %v", err)
	}
	if importEvidence.Metadata.Namespace != "mss-shop-dev" ||
		importEvidence.Spec.TargetDatabase != "mss_shop_dev" ||
		importEvidence.Spec.InitialMarker != "r1shop.io/operator-binding=mss-shop-dev:PostgreSQL:mss_shop_dev;state=isolated-empty" ||
		len(importEvidence.Spec.Attempts) != len(legacyImport.Attempts[:2]) ||
		importEvidence.Spec.CurrentState.SuccessfulImport ||
		importEvidence.Spec.CurrentState.TargetWritesFromAttempts ||
		importEvidence.Spec.CurrentState.ReceiptEmitted {
		t.Fatalf("isolated import attempt evidence drifted: %#v", importEvidence)
	}
	for index, attempt := range importEvidence.Spec.Attempts {
		if attempt.Revision != legacyImport.Attempts[index].Revision ||
			attempt.ImageDigest != legacyImport.Attempts[index].ImageDigest ||
			attempt.Readiness.Result != "passed" ||
			attempt.Importer.FailureStage != "before-target-transaction" ||
			attempt.Importer.TargetTransactionStarted || attempt.Importer.ReceiptEmitted {
			t.Fatalf("isolated import evidence attempt %d drifted: %#v", index+1, attempt)
		}
		outputDigest := sha256.Sum256([]byte(attempt.Importer.CompleteOutput))
		if hex.EncodeToString(outputDigest[:]) != attempt.Importer.CompleteOutputSHA256 {
			t.Fatalf("isolated import evidence attempt %d output digest drifted", index+1)
		}
	}

	successEvidenceContent, err := os.ReadFile("../" + legacyImport.SuccessEvidence)
	if err != nil {
		t.Fatalf("read isolated import success evidence: %v", err)
	}
	successEvidenceFileDigest := sha256.Sum256(successEvidenceContent)
	if hex.EncodeToString(successEvidenceFileDigest[:]) != legacyImport.SuccessEvidenceFileSHA256 {
		t.Fatal("isolated import success evidence file digest drifted")
	}
	var successEvidence struct {
		Metadata struct {
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			SourceRevision string `yaml:"sourceRevision"`
			TargetDatabase string `yaml:"targetDatabase"`
			Image          struct {
				Repository string `yaml:"repository"`
				Digest     string `yaml:"digest"`
			} `yaml:"image"`
			OriginalDevelopmentBoundary struct {
				Environment                  string `yaml:"environment"`
				EvidencePath                 string `yaml:"evidencePath"`
				BeforeFileSHA256             string `yaml:"beforeFileSHA256"`
				AfterFileSHA256              string `yaml:"afterFileSHA256"`
				ByteIdenticalBeforeAndAfter  bool   `yaml:"byteIdenticalBeforeAndAfter"`
				SelectedSafeFieldsSHA256     string `yaml:"selectedSafeFieldsSHA256"`
				SecretsAccessed              bool   `yaml:"secretsAccessed"`
				DatabaseConnectionsPerformed bool   `yaml:"databaseConnectionsPerformed"`
				WritesPerformed              bool   `yaml:"writesPerformed"`
			} `yaml:"originalDevelopmentBoundary"`
			Readiness struct {
				Job              string `yaml:"job"`
				JobUID           string `yaml:"jobUID"`
				Pod              string `yaml:"pod"`
				PodUID           string `yaml:"podUID"`
				Result           string `yaml:"result"`
				RestartCount     int    `yaml:"restartCount"`
				StdoutSHA256     string `yaml:"stdoutSHA256"`
				PodBindingSHA256 string `yaml:"podBindingSHA256"`
				JobBindingSHA256 string `yaml:"jobBindingSHA256"`
			} `yaml:"readiness"`
			Importer struct {
				Job                     string `yaml:"job"`
				JobUID                  string `yaml:"jobUID"`
				Pod                     string `yaml:"pod"`
				PodUID                  string `yaml:"podUID"`
				Result                  string `yaml:"result"`
				RestartCount            int    `yaml:"restartCount"`
				Retries                 int    `yaml:"retries"`
				StdoutReceiptFileSHA256 string `yaml:"stdoutReceiptFileSHA256"`
				PodBindingSHA256        string `yaml:"podBindingSHA256"`
				JobBindingSHA256        string `yaml:"jobBindingSHA256"`
			} `yaml:"importer"`
			Receipt struct {
				EvidencePath           string `yaml:"evidencePath"`
				FileSHA256             string `yaml:"fileSHA256"`
				CanonicalPayloadSHA256 string `yaml:"canonicalPayloadSHA256"`
				ManifestSHA256         string `yaml:"manifestSHA256"`
				SchemaSHA256           string `yaml:"schemaSHA256"`
				TableCount             int    `yaml:"tableCount"`
				CopiedTableCount       int    `yaml:"copiedTableCount"`
				StructureOnly          map[string]struct {
					SourceRows int64 `yaml:"sourceRows"`
					TargetRows int64 `yaml:"targetRows"`
				} `yaml:"structureOnly"`
				DatabaseMarker string `yaml:"databaseMarker"`
			} `yaml:"receipt"`
			DeliveryCI struct {
				RunID                int64  `yaml:"runID"`
				Conclusion           string `yaml:"conclusion"`
				TenantDigest         string `yaml:"tenantDigest"`
				MallDigest           string `yaml:"mallDigest"`
				ReconcilerDigest     string `yaml:"reconcilerDigest"`
				LegacyImporterDigest string `yaml:"legacyImporterDigest"`
			} `yaml:"deliveryCI"`
			CurrentState struct {
				SuccessfulImport                        bool  `yaml:"successfulImport"`
				ReceiptPersisted                        bool  `yaml:"receiptPersisted"`
				TargetOrderRows                         int64 `yaml:"targetOrderRows"`
				TargetOrderGoodsRows                    int64 `yaml:"targetOrderGoodsRows"`
				PostImportVerifierExecuted              bool  `yaml:"postImportVerifierExecuted"`
				ReconciliationExecuted                  bool  `yaml:"reconciliationExecuted"`
				AdminRuntimeDeployed                    bool  `yaml:"adminRuntimeDeployed"`
				OriginalDevelopmentEnvironmentUnchanged bool  `yaml:"originalDevelopmentEnvironmentUnchanged"`
				ProductionResourcesAccessed             bool  `yaml:"productionResourcesAccessed"`
			} `yaml:"currentState"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(successEvidenceContent, &successEvidence); err != nil {
		t.Fatalf("parse isolated import success evidence: %v", err)
	}
	successSpec := successEvidence.Spec
	boundary := successSpec.OriginalDevelopmentBoundary
	if successEvidence.Metadata.Namespace != "mss-shop-dev" ||
		successSpec.SourceRevision != successfulAttempt.Revision || successSpec.TargetDatabase != "mss_shop_dev" ||
		successSpec.Image.Repository != "ghcr.io/shop-r1/mss-shop-legacy-importer" ||
		successSpec.Image.Digest != successfulAttempt.ImageDigest ||
		boundary.Environment != "r1shop-dev-read-only" ||
		boundary.EvidencePath != "docs/evidence/original-dev/2026-09-01-import-boundary-6fed45f.json" ||
		boundary.BeforeFileSHA256 != "70b29137f5c499c8819effb4313838a5fd73f0d229205ed92160dda43663683d" ||
		boundary.AfterFileSHA256 != "70b29137f5c499c8819effb4313838a5fd73f0d229205ed92160dda43663683d" ||
		!boundary.ByteIdenticalBeforeAndAfter || boundary.SelectedSafeFieldsSHA256 != originalDevSafeFieldsSHA256 ||
		boundary.SecretsAccessed ||
		boundary.DatabaseConnectionsPerformed || boundary.WritesPerformed {
		t.Fatalf("isolated import source or original-development boundary drifted: %#v", successSpec)
	}
	if successSpec.Readiness.Job != "mss-shop-readiness-6fed45f354e93efe104045c6dde86ac33c368d6d" ||
		successSpec.Readiness.JobUID != "a806992f-5152-40fb-b566-859b6388b737" ||
		successSpec.Readiness.Pod != "mss-shop-readiness-6fed45f354e93efe104045c6dde86ac33c368d6ws2rz" ||
		successSpec.Readiness.PodUID != "85310922-3b41-45d0-8e83-bbbe4efc7f52" ||
		successSpec.Readiness.Result != "passed" || successSpec.Readiness.RestartCount != 0 ||
		successSpec.Readiness.StdoutSHA256 != "f57f3568a566a9f38c70936c8da6fa15a1ec2b043b6e8d7ea410cf6d9a91f91e" ||
		successSpec.Readiness.PodBindingSHA256 != "ca08a0ec3c66025d1db4aeb78b71667af02373f489af5bb781657190e7a2595b" ||
		successSpec.Readiness.JobBindingSHA256 != "79773cc1b2f0a70155da14604d5748d4273865ed5ec69ae46930979bfb122b4b" {
		t.Fatalf("successful readiness provenance drifted: %#v", successSpec.Readiness)
	}
	if successSpec.Importer.Job != successfulAttempt.ImporterJob || successSpec.Importer.Result != "succeeded" ||
		successSpec.Importer.JobUID != "40f0e23d-659f-4bba-a8c9-21d530fbab95" ||
		successSpec.Importer.Pod != "mss-shop-legacy-import-6fed45f354e93efe104045c6dde86ac33c3cm9ml" ||
		successSpec.Importer.PodUID != "efa711bb-d31c-4762-ad6a-478ea190dd3e" ||
		successSpec.Importer.RestartCount != 0 || successSpec.Importer.Retries != 0 ||
		successSpec.Importer.StdoutReceiptFileSHA256 != legacyImport.ReceiptFileSHA256 ||
		successSpec.Importer.PodBindingSHA256 != "67737edb16fc83878aa422c82abe5547463a1506aac8ef584260b5f77253446b" ||
		successSpec.Importer.JobBindingSHA256 != "d7eb67c182ba82df4fa4e6161c8acd219ad06b2806e3e09749f49b87f274ba99" {
		t.Fatalf("successful importer provenance drifted: %#v", successSpec.Importer)
	}
	receiptEvidence := successSpec.Receipt
	if receiptEvidence.EvidencePath != legacyImport.Receipt || receiptEvidence.FileSHA256 != legacyImport.ReceiptFileSHA256 ||
		receiptEvidence.CanonicalPayloadSHA256 != legacyImport.ReceiptSHA256 ||
		receiptEvidence.ManifestSHA256 != "c108b11543f41bbd8384540b7314909cd8056e3a141cc7447c443cb98c7e6e5b" ||
		receiptEvidence.SchemaSHA256 != "afd144a9654602a2452dc20e758b4cc3947a3e0748941ea58da7a53d2874a93a" ||
		receiptEvidence.TableCount != 51 || receiptEvidence.CopiedTableCount != 49 ||
		receiptEvidence.DatabaseMarker != legacyImport.CurrentMarker || len(receiptEvidence.StructureOnly) != 2 ||
		receiptEvidence.StructureOnly["orders"].SourceRows != 14 || receiptEvidence.StructureOnly["orders"].TargetRows != 0 ||
		receiptEvidence.StructureOnly["order_goods"].SourceRows != 1170 || receiptEvidence.StructureOnly["order_goods"].TargetRows != 0 {
		t.Fatalf("successful import receipt summary drifted: %#v", receiptEvidence)
	}
	if successSpec.DeliveryCI.RunID != 33494258866 || successSpec.DeliveryCI.Conclusion != "success" ||
		successSpec.DeliveryCI.TenantDigest != "sha256:fe8db92bce70c12be7d0f6ad8de60e05d65322c2b3aecf384d212deb35abe3fd" ||
		successSpec.DeliveryCI.MallDigest != "sha256:4db62ab4c264714fd0c23a1033935d569edfd9f54c1c37d4f58f6299df50d925" ||
		successSpec.DeliveryCI.ReconcilerDigest != "sha256:8cb1e54946b442efd5f33fc433b06bd9e663ec85c81091b8518861855b92e781" ||
		successSpec.DeliveryCI.LegacyImporterDigest != successfulAttempt.ImageDigest {
		t.Fatalf("successful import CI provenance drifted: %#v", successSpec.DeliveryCI)
	}
	currentState := successSpec.CurrentState
	if !currentState.SuccessfulImport || !currentState.ReceiptPersisted || currentState.TargetOrderRows != 0 ||
		currentState.TargetOrderGoodsRows != 0 || currentState.PostImportVerifierExecuted ||
		currentState.ReconciliationExecuted || currentState.AdminRuntimeDeployed ||
		!currentState.OriginalDevelopmentEnvironmentUnchanged || currentState.ProductionResourcesAccessed {
		t.Fatalf("successful import current state drifted: %#v", currentState)
	}

	type receiptTable struct {
		Name         string `json:"name"`
		Mode         string `json:"mode"`
		SourceRows   int64  `json:"sourceRows"`
		TargetRows   int64  `json:"targetRows"`
		SourceSHA256 string `json:"sourceSHA256"`
		TargetSHA256 string `json:"targetSHA256"`
	}
	type receiptDocument struct {
		Version        string         `json:"version"`
		TargetDatabase string         `json:"targetDatabase"`
		ManifestSHA256 string         `json:"manifestSHA256"`
		SchemaSHA256   string         `json:"schemaSHA256"`
		Tables         []receiptTable `json:"tables"`
		SHA256         string         `json:"sha256"`
	}
	receiptContent, err := os.ReadFile("../" + legacyImport.Receipt)
	if err != nil {
		t.Fatalf("read committed legacy import receipt: %v", err)
	}
	receiptFileDigest := sha256.Sum256(receiptContent)
	if hex.EncodeToString(receiptFileDigest[:]) != legacyImport.ReceiptFileSHA256 {
		t.Fatal("committed legacy import receipt file digest drifted")
	}
	decoder := json.NewDecoder(bytes.NewReader(receiptContent))
	decoder.DisallowUnknownFields()
	var receipt receiptDocument
	if err := decoder.Decode(&receipt); err != nil {
		t.Fatalf("parse committed legacy import receipt: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatal("committed legacy import receipt has trailing JSON")
	}
	if receipt.Version != "mss-shop-legacy-import/v1" || receipt.TargetDatabase != "mss_shop_dev" ||
		receipt.ManifestSHA256 != receiptEvidence.ManifestSHA256 || receipt.SchemaSHA256 != receiptEvidence.SchemaSHA256 ||
		receipt.SHA256 != legacyImport.ReceiptSHA256 || len(receipt.Tables) != 51 {
		t.Fatalf("committed legacy import receipt header drifted: %#v", receipt)
	}
	manifestContent, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest for receipt order: %v", err)
	}
	var tableManifest legacyTableManifest
	if err := yaml.Unmarshal(manifestContent, &tableManifest); err != nil {
		t.Fatalf("parse legacy table manifest for receipt order: %v", err)
	}
	wantReceiptNames := make([]string, 0, 51)
	for _, table := range tableManifest.Spec.Tables {
		if table.Name != "roles" && table.Name != "tenants" && table.Name != "users" {
			wantReceiptNames = append(wantReceiptNames, table.Name)
		}
	}
	copiedTables := 0
	structureOnlyTables := make(map[string]receiptTable, 2)
	seenReceiptTables := make(map[string]struct{}, len(receipt.Tables))
	for index, table := range receipt.Tables {
		if table.Name != wantReceiptNames[index] {
			t.Fatalf("receipt table order %d = %q, want %q", index, table.Name, wantReceiptNames[index])
		}
		if _, duplicate := seenReceiptTables[table.Name]; duplicate {
			t.Fatalf("duplicate receipt table %q", table.Name)
		}
		seenReceiptTables[table.Name] = struct{}{}
		switch table.Mode {
		case "copied":
			copiedTables++
			if table.SourceRows != table.TargetRows || table.SourceSHA256 != table.TargetSHA256 {
				t.Fatalf("copied receipt table differs: %#v", table)
			}
		case "structure-only":
			if table.TargetRows != 0 {
				t.Fatalf("structure-only receipt table is nonempty: %#v", table)
			}
			structureOnlyTables[table.Name] = table
		default:
			t.Fatalf("unknown receipt table mode: %#v", table)
		}
	}
	if copiedTables != 49 || len(structureOnlyTables) != 2 ||
		structureOnlyTables["orders"].SourceRows != 14 ||
		structureOnlyTables["order_goods"].SourceRows != 1170 {
		t.Fatalf("receipt table modes drifted: copied=%d structureOnly=%#v", copiedTables, structureOnlyTables)
	}
	canonicalPayload, err := json.Marshal(struct {
		Version        string         `json:"version"`
		TargetDatabase string         `json:"targetDatabase"`
		ManifestSHA256 string         `json:"manifestSHA256"`
		SchemaSHA256   string         `json:"schemaSHA256"`
		Tables         []receiptTable `json:"tables"`
	}{receipt.Version, receipt.TargetDatabase, receipt.ManifestSHA256, receipt.SchemaSHA256, receipt.Tables})
	if err != nil {
		t.Fatalf("canonicalize committed legacy import receipt: %v", err)
	}
	canonicalDigest := sha256.Sum256(canonicalPayload)
	if hex.EncodeToString(canonicalDigest[:]) != legacyImport.ReceiptSHA256 {
		t.Fatal("committed legacy import receipt canonical payload digest drifted")
	}
	verificationRelative := "docs/evidence/legacy-import/" + legacyReceiptSHA256 + "/verification.json"
	verifierEvidenceRelative := "docs/evidence/mss-shop-dev/2026-09-01-verifier-success.yaml"
	verifierState := evidence.PostImportVerifier
	if verifierState.Revision != legacyVerifierRevision || verifierState.Job != legacyVerifierJobName ||
		verifierState.JobUID != legacyVerifierJobUID || verifierState.Pod != legacyVerifierPod ||
		verifierState.PodUID != legacyVerifierPodUID || verifierState.ImageDigest != legacyVerifierDigest ||
		verifierState.Result != "succeeded" || verifierState.Verification != verificationRelative ||
		verifierState.VerificationFileSHA256 != legacyVerificationFileSHA256 ||
		verifierState.SuccessEvidence != verifierEvidenceRelative ||
		verifierState.SuccessEvidenceFileSHA256 != legacyVerifierEvidenceSHA256 ||
		verifierState.AnnotationCompatibilityFixRevision != annotationFixRevision ||
		!verifierState.ReadOnlyExactRetryValidated || verifierState.NewRevisionVerifierJobCreated ||
		verifierState.NewRevisionVerifierPodCreated || verifierState.ReceiptConfigMapChangedByFix ||
		verifierState.KubernetesWritesFromFixValidation {
		t.Fatalf("post-import verifier state drifted: %#v", verifierState)
	}
	verificationContent, err := os.ReadFile("../" + verificationRelative)
	if err != nil {
		t.Fatalf("read committed post-import verification: %v", err)
	}
	verificationFileDigest := sha256.Sum256(verificationContent)
	if hex.EncodeToString(verificationFileDigest[:]) != legacyVerificationFileSHA256 {
		t.Fatal("committed post-import verification file digest drifted")
	}
	verification, err := decodeExactLegacyVerification(verificationContent)
	if err != nil {
		t.Fatalf("parse committed post-import verification: %v", err)
	}
	verifierImageRepository := "ghcr.io/shop-r1/mss-shop-legacy-importer"
	verifierImageReference := verifierImageRepository + ":" + legacyVerifierRevision + "@" + legacyVerifierDigest
	if verification.Version != "mss-shop-disposable-verification/v1" ||
		verification.TargetDatabase != "mss_shop_dev" ||
		verification.DatabaseMarker != "mss-shop-isolated-dev:legacy-import:v1:"+legacyReceiptSHA256 ||
		verification.ReceiptSHA256 != legacyReceiptSHA256 ||
		verification.ManifestSHA256 != legacyManifestSHA256 || verification.SchemaSHA256 != legacySchemaSHA256 ||
		verification.TableCount != 51 || verification.OrdersRows != 0 || verification.OrderGoodsRows != 0 ||
		verification.Namespace != "mss-shop-dev" || verification.PodName != legacyVerifierPod ||
		verification.PodUID != legacyVerifierPodUID || verification.Revision != legacyVerifierRevision ||
		verification.ImageRepository != verifierImageRepository || verification.ImageDigest != legacyVerifierDigest ||
		verification.ImageReference != verifierImageReference {
		t.Fatalf("committed post-import verification drifted: %#v", verification)
	}

	verifierEvidenceContent, err := os.ReadFile("../" + verifierEvidenceRelative)
	if err != nil {
		t.Fatalf("read post-import verifier summary evidence: %v", err)
	}
	verifierEvidenceFileDigest := sha256.Sum256(verifierEvidenceContent)
	if hex.EncodeToString(verifierEvidenceFileDigest[:]) != legacyVerifierEvidenceSHA256 {
		t.Fatal("post-import verifier summary evidence file digest drifted")
	}
	var verifierEvidence legacyVerifierSuccessEvidence
	verifierEvidenceDecoder := yaml.NewDecoder(bytes.NewReader(verifierEvidenceContent))
	verifierEvidenceDecoder.KnownFields(true)
	if err := verifierEvidenceDecoder.Decode(&verifierEvidence); err != nil {
		t.Fatalf("parse post-import verifier summary evidence: %v", err)
	}
	var verifierEvidenceTrailing any
	if err := verifierEvidenceDecoder.Decode(&verifierEvidenceTrailing); !errors.Is(err, io.EOF) {
		t.Fatal("post-import verifier summary evidence has trailing YAML documents")
	}
	if verifierEvidence.APIVersion != "r1shop.io/v1alpha1" ||
		verifierEvidence.Kind != "IsolatedLegacyImportVerificationEvidence" ||
		verifierEvidence.Metadata.Namespace != "mss-shop-dev" ||
		verifierEvidence.Metadata.CapturedAt != "2026-09-01" {
		t.Fatalf("post-import verifier summary header drifted: %#v", verifierEvidence)
	}
	verificationSpec := verifierEvidence.Spec
	verificationReceipt := verificationSpec.Receipt
	if verificationReceipt.CanonicalPayloadSHA256 != legacyReceiptSHA256 ||
		verificationReceipt.FileSHA256 != legacyReceiptFileSHA256 ||
		verificationReceipt.ManifestSHA256 != legacyManifestSHA256 ||
		verificationReceipt.SchemaSHA256 != legacySchemaSHA256 ||
		verificationReceipt.DatabaseMarker != verification.DatabaseMarker {
		t.Fatalf("post-import verifier receipt binding drifted: %#v", verificationReceipt)
	}
	configMapEvidence := verificationSpec.ImmutableEvidenceConfigMap
	if configMapEvidence.Name != "mss-shop-legacy-import-receipt" ||
		configMapEvidence.UID != "699d5d42-0e3a-4535-8182-8fae5829ea7c" ||
		configMapEvidence.CreatedAt != "2026-09-01T10:32:48Z" ||
		configMapEvidence.Revision != legacyVerifierRevision || !configMapEvidence.Immutable ||
		!reflect.DeepEqual(configMapEvidence.DataKeys, []string{"receipt.json"}) ||
		configMapEvidence.ReceiptFileSHA256 != legacyReceiptFileSHA256 ||
		configMapEvidence.SafeBindingSHA256 != "d257223d454e276d1e3303f567820cdc2b49b9a648f5ee4a83287fad1aa29afc" {
		t.Fatalf("immutable receipt ConfigMap evidence drifted: %#v", configMapEvidence)
	}
	verifierWorkload := verificationSpec.Verifier
	if verifierWorkload.Revision != legacyVerifierRevision || verifierWorkload.Job != legacyVerifierJobName ||
		verifierWorkload.JobUID != legacyVerifierJobUID || verifierWorkload.JobCreatedAt != "2026-09-01T10:32:49Z" ||
		verifierWorkload.JobCompletedAt != "2026-09-01T10:33:04Z" ||
		verifierWorkload.JobSucceeded != 1 || verifierWorkload.JobFailed != 0 ||
		verifierWorkload.Pod != legacyVerifierPod || verifierWorkload.PodUID != legacyVerifierPodUID ||
		verifierWorkload.PodPhase != "Succeeded" || verifierWorkload.RestartCount != 0 || verifierWorkload.Retries != 0 ||
		verifierWorkload.ImageRepository != verifierImageRepository ||
		verifierWorkload.ImageDigest != legacyVerifierDigest || verifierWorkload.ImageReference != verifierImageReference ||
		verifierWorkload.RuntimeImageID != verifierImageRepository+"@"+legacyVerifierDigest ||
		verifierWorkload.OutputPath != verificationRelative ||
		verifierWorkload.OutputFileSHA256 != legacyVerificationFileSHA256 ||
		verifierWorkload.JobBindingSHA256 != "5be650ea0455b9267b8407baa53a7d8ac6a8a7b4106423e9b43af0193bf1775c" ||
		verifierWorkload.PodBindingSHA256 != "9c7d64e226bd05ccb287fdf9da7dadf0c23a456a1234ab8247e934d631d63771" ||
		verifierWorkload.TargetDatabase != verification.TargetDatabase ||
		verifierWorkload.TableCount != verification.TableCount || verifierWorkload.OrdersRows != 0 ||
		verifierWorkload.OrderGoodsRows != 0 || verifierWorkload.Result != "succeeded" {
		t.Fatalf("post-import verifier workload evidence drifted: %#v", verifierWorkload)
	}
	networkBoundary := verificationSpec.NetworkBoundary
	if networkBoundary.Policy != "allow-database-writers-to-postgres-egress" ||
		networkBoundary.PolicyUID != "ba6b2866-5f90-4ea8-bcc7-57bd84abb71e" ||
		networkBoundary.SafeBindingSHA256 != "1e7374fbbec74cf3cb333df3a79a70da7a97207d8a3530d7249a4287cff33c7b" ||
		networkBoundary.Target != "mss-shop-dev/mss-shop-postgres:5432" ||
		networkBoundary.LegacySourceCredentialMounted || networkBoundary.LegacySourceEndpointPresent ||
		networkBoundary.RedisCredentialMounted || networkBoundary.KubernetesServiceAccountTokenMounted {
		t.Fatalf("post-import verifier network boundary is unsafe: %#v", networkBoundary)
	}
	stageHistory := verificationSpec.StageHistory
	initialCreate := stageHistory.InitialCreate
	if initialCreate.OperatorRevision != legacyVerifierRevision ||
		initialCreate.Result != "workload-succeeded-stage-post-create-check-failed" ||
		initialCreate.FailureReason != "unreviewed-kubesphere-revisions-annotation" ||
		!initialCreate.ConfigMapCreated || !initialCreate.VerifierJobCreated ||
		initialCreate.VerifierOutputAcceptedAtThatTime || !initialCreate.ResourcesPreserved {
		t.Fatalf("initial verifier stage history drifted: %#v", initialCreate)
	}
	annotationFix := stageHistory.AnnotationCompatibilityFix
	if annotationFix.Revision != annotationFixRevision || annotationFix.ExactKubeSphereVersion != "v3.1.1" ||
		!annotationFix.SourceTestsPassed || annotationFix.IndependentReviewBlockers != 0 {
		t.Fatalf("annotation compatibility fix evidence drifted: %#v", annotationFix)
	}
	rejectedNewRevision := stageHistory.RejectedNewRevisionDryRun
	if rejectedNewRevision.TargetRevision != annotationFixRevision || rejectedNewRevision.Result != "failed-closed" ||
		rejectedNewRevision.Reason != "immutable-receipt-configmap-remains-bound-to-verifier-revision" ||
		rejectedNewRevision.KubernetesWritesPerformed || rejectedNewRevision.ReceiptConfigMapChanged ||
		rejectedNewRevision.NewVerifierJobCreated || rejectedNewRevision.NewVerifierPodCreated {
		t.Fatalf("new-revision verifier rejection evidence drifted: %#v", rejectedNewRevision)
	}
	exactRetry := stageHistory.ReadOnlyExactRetryAudit
	if exactRetry.OperatorRevision != annotationFixRevision || exactRetry.TargetRevision != legacyVerifierRevision ||
		exactRetry.OperatorBinarySHA256 != "5f5dacb7674d6b2f03470e255ca65ca415a389f6136aab2013139a676d4b5087" ||
		exactRetry.StageLogSHA256 != "2924316b98542f33453fd3a21aa6bbcdcef9d768548f5478710f7e49d19d3e46" ||
		exactRetry.CreateFlag || exactRetry.Created || !exactRetry.ExactRetry || exactRetry.DryRun ||
		exactRetry.KubernetesWritesPerformed || exactRetry.Result != "passed" {
		t.Fatalf("read-only exact verifier retry evidence drifted: %#v", exactRetry)
	}
	originalBoundary := verificationSpec.OriginalDevelopmentBoundary
	if originalBoundary.Environment != "r1shop-dev-read-only" ||
		originalBoundary.EvidencePath != fingerprint.EvidencePath ||
		originalBoundary.EvidenceFileSHA256 != fingerprint.EvidenceFileSHA256 ||
		originalBoundary.SelectedSafeFieldsSHA256 != originalDevSafeFieldsSHA256 ||
		!originalBoundary.UnchangedFromImportBoundary || originalBoundary.SecretsAccessed ||
		originalBoundary.DatabaseConnectionsPerformed || originalBoundary.WritesPerformed {
		t.Fatalf("post-verifier original development boundary is unsafe: %#v", originalBoundary)
	}
	verifierCurrentState := verificationSpec.CurrentState
	if !verifierCurrentState.PostImportVerifierExecuted || verifierCurrentState.ReconciliationExecuted ||
		verifierCurrentState.AdminRuntimeDeployed || verifierCurrentState.IsolatedBrowserAcceptanceExecuted ||
		verifierCurrentState.BusinessAcceptanceScenariosClosed != 0 ||
		verifierCurrentState.BusinessAcceptanceScenariosRequired != 31 ||
		!verifierCurrentState.OriginalDevelopmentEnvironmentUnchanged ||
		verifierCurrentState.ProductionResourcesAccessed {
		t.Fatalf("post-import verifier current state overclaims acceptance: %#v", verifierCurrentState)
	}
	if status.Spec.LocalEvidence.MallAdminWeb.ResourceRoutes != 50 || status.Spec.LocalEvidence.MallAdminWeb.OwnershipDeployment != "pending-forward-migrations-not-applied" {
		t.Fatalf("mall source/deployment state drifted: %#v", status.Spec.LocalEvidence.MallAdminWeb)
	}
	if status.Spec.LocalEvidence.TenantAdminWeb.ResourceRoutes != 1 || status.Spec.LocalEvidence.TenantAdminWeb.TargetResource != "payments" || status.Spec.LocalEvidence.TenantAdminWeb.OwnershipDeployment != "pending-forward-migrations-not-applied" {
		t.Fatalf("tenant source/deployment state drifted: %#v", status.Spec.LocalEvidence.TenantAdminWeb)
	}
	if inventory.AdminBusinessViews != 54 || inventory.LegacyHTTPRequests != 185 || inventory.LegacyButtonPermissions != 171 {
		t.Fatalf("legacy Admin inventory drifted: %#v", inventory)
	}

	matrix, err := os.ReadFile("../docs/migration/legacy-admin-acceptance-matrix.md")
	if err != nil {
		t.Fatalf("read legacy acceptance matrix: %v", err)
	}
	scenarioPattern := regexp.MustCompile(`(?m)^\| [A-Z0-9]+-[0-9]{3} \|`)
	if actual := len(scenarioPattern.FindAll(matrix, -1)); actual != inventory.RequiredAcceptanceScenarios {
		t.Fatalf("acceptance scenarios = %d, status records %d", actual, inventory.RequiredAcceptanceScenarios)
	}
	if status.Spec.Acceptance.ClosedScenarios != 0 {
		t.Fatalf("closed acceptance scenarios = %d without system evidence", status.Spec.Acceptance.ClosedScenarios)
	}
	validation := status.Spec.LocalEvidence.CurrentSourceValidation
	resources := validation.IsolatedCreateOnlyResources
	if resources.InfrastructureObjects != 24 || resources.FoundationSecrets != 6 ||
		resources.SuccessfulReadinessJobs != 3 || resources.FailedImporterJobs != 2 ||
		resources.SuccessfulImporterJobs != 1 || resources.ReceiptEvidenceConfigMaps != 1 ||
		resources.SuccessfulVerifierJobs != 1 ||
		resources.Total != resources.InfrastructureObjects+resources.FoundationSecrets+
			resources.SuccessfulReadinessJobs+resources.FailedImporterJobs+resources.SuccessfulImporterJobs+
			resources.ReceiptEvidenceConfigMaps+resources.SuccessfulVerifierJobs || resources.Total != 38 ||
		!validation.OriginalDevelopmentReady ||
		validation.OriginalDevelopmentSelectedSafeFieldsSHA256 != originalDevSafeFieldsSHA256 ||
		!validation.IsolatedNamespaceExists || !validation.IsolatedPostgreSQLReady ||
		!validation.IsolatedRedisReady || !validation.SuccessfulLegacyImport ||
		!validation.SuccessfulPostImportVerifier {
		t.Fatalf("current isolated validation evidence drifted: %#v", validation)
	}
	images := status.Spec.LocalEvidence.DeliveryImages
	verifiedRun := images.VerifiedRun
	if images.CurrentFourImagePublication != "verified-revision-c-fc6d1bf-not-deployed" ||
		verifiedRun.ID != 33503127917 ||
		verifiedRun.Revision != "fc6d1bf357ca7291a0fc2fe4391ca15628f8e9b9" ||
		verifiedRun.Conclusion != "success" ||
		verifiedRun.Scope != "revision-c-verifier-evidence-publication-not-deployed" ||
		verifiedRun.TenantDigest != "sha256:87a2ba402b9dc5f82769b4fbf4c1b1220368483dde6a4c6fc580507328f05750" ||
		verifiedRun.MallDigest != "sha256:22a02242cc815ec7e2bf29fc5f9ec86789a245074754f49b59bc2b7def66c92e" ||
		verifiedRun.ReconcilerDigest != "sha256:0beece2f39be8892649981db69bb20ebef6c6b27a6ab8741d4cf129b5d6a3af5" ||
		verifiedRun.LegacyImporterDigest != "sha256:385e5161b5a2133a482cb34330092d4e94e9e3c48d7b1a7ca01dc5f4b7e3bb38" {
		t.Fatalf("verified four-image evidence drifted: %#v", images)
	}
	verifierRun := images.VerifierRun
	if verifierRun.ID != 33497583981 || verifierRun.Revision != legacyVerifierRevision ||
		verifierRun.Conclusion != "success" || verifierRun.Scope != "successful-post-import-verifier-revision-b" ||
		verifierRun.TenantDigest != "sha256:69e790e145e81c8d1586d8c829ba45152cdfd14d508beb2c3b758bcfb4b57e43" ||
		verifierRun.MallDigest != "sha256:e5415ff0dec41d08d315d74426b788e4c7cb189a39ee5a587d04364e37289f40" ||
		verifierRun.ReconcilerDigest != "sha256:abb9703daeaf8bab16bd6c020390b1da1de1bce31e664589a1c67749c46ec810" ||
		verifierRun.LegacyImporterDigest != legacyVerifierDigest {
		t.Fatalf("revision-B verifier delivery evidence drifted: %#v", images)
	}
	verifierDeliveryCI := verificationSpec.DeliveryCI.VerifierWorkload
	if verifierDeliveryCI.RunID != verifierRun.ID || verifierDeliveryCI.Revision != verifierRun.Revision ||
		verifierDeliveryCI.Conclusion != verifierRun.Conclusion ||
		verifierDeliveryCI.TenantDigest != verifierRun.TenantDigest ||
		verifierDeliveryCI.MallDigest != verifierRun.MallDigest ||
		verifierDeliveryCI.ReconcilerDigest != verifierRun.ReconcilerDigest ||
		verifierDeliveryCI.LegacyImporterDigest != verifierRun.LegacyImporterDigest {
		t.Fatalf("verifier summary CI binding drifted: %#v", verifierDeliveryCI)
	}
	fixDeliveryCI := verificationSpec.DeliveryCI.AnnotationCompatibilityFix
	if fixDeliveryCI.RunID != 33500133380 || fixDeliveryCI.Revision != annotationFixRevision ||
		fixDeliveryCI.Conclusion != "success" || fixDeliveryCI.Deployed ||
		fixDeliveryCI.TenantDigest != "sha256:9fe3463cbf88e4312f5b6735e25470d62fe5c9eb685f8cd8d83a3e7b57a34467" ||
		fixDeliveryCI.MallDigest != "sha256:636159961ab6c0bc9303ed9e9ef759dea30510ca9a84c3e33d829e636d678a2c" ||
		fixDeliveryCI.ReconcilerDigest != "sha256:3afa8204a0f14dfbbe1fd9510d1c0a0e0cd0153f13dc4e3421bde56810705baf" ||
		fixDeliveryCI.LegacyImporterDigest != "sha256:80a382e42aa43d49c2e4431798d72699d12077106c9fa7755466df28e822adc3" {
		t.Fatalf("annotation-fix summary CI binding drifted: %#v", fixDeliveryCI)
	}

	requiredSources := []string{
		"architecture", "invariants", "decision", "qualificationDecision", "dataContract", "tableManifest",
		"acceptanceMatrix", "implementationSkill", "deliveryChecklist",
		"memoryGovernance", "memoryCheck", "mcpIntegration", "mcpConfiguration",
		"mss137GenerationNotes", "localBrowserAcceptance",
		"remoteDevelopmentDecision", "remoteDevelopmentRunbook", "catalogLogisticsDesignReview",
		"originalDevelopmentEvidence", "postVerifierOriginalDevelopmentEvidence",
		"isolatedImportAttemptEvidence", "isolatedImportSuccessEvidence", "isolatedImportReceipt",
		"isolatedVerifierEvidence", "isolatedVerification", "memberLevelsDevelopment",
	}
	for _, name := range requiredSources {
		if _, exists := status.Spec.SourcesOfTruth[name]; !exists {
			t.Errorf("required source of truth %q is not registered", name)
		}
	}
	for name, source := range status.Spec.SourcesOfTruth {
		if source == "" {
			t.Fatalf("source of truth %q is empty", name)
		}
		info, err := os.Stat("../" + source)
		if err != nil {
			t.Fatalf("source of truth %q (%s): %v", name, source, err)
		}
		if name == "memoryCheck" && info.Mode()&0o111 == 0 {
			t.Fatalf("memory check %q is not executable", source)
		}
	}
	if status.Spec.SourcesOfTruth["decision"] != "docs/decisions/DEC-0009-tenant-owned-catalog-and-logistics.md" ||
		status.Spec.SourcesOfTruth["qualificationDecision"] != "docs/decisions/DEC-0007-qualified-legacy-business-contract.md" ||
		status.Spec.SourcesOfTruth["originalDevelopmentEvidence"] != "docs/evidence/original-dev/2026-09-01-import-boundary-6fed45f.json" ||
		status.Spec.SourcesOfTruth["postVerifierOriginalDevelopmentEvidence"] != fingerprint.EvidencePath ||
		status.Spec.SourcesOfTruth["isolatedImportReceipt"] != legacyImport.Receipt ||
		status.Spec.SourcesOfTruth["isolatedVerifierEvidence"] != verifierEvidenceRelative ||
		status.Spec.SourcesOfTruth["isolatedVerification"] != verificationRelative {
		t.Fatalf("legacy decisions drifted: %#v", status.Spec.SourcesOfTruth)
	}
	safety := status.Spec.Safety
	if !safety.ProductionReadOnly || !safety.OriginalDevelopmentEnvironmentImmutable ||
		safety.IsolatedDevelopmentWriteNamespace != "mss-shop-dev" ||
		!safety.OldDevelopmentWritesForbidden || safety.ProductionOrderRowsToDevelopment ||
		safety.TargetOrderRowsImported || safety.TargetOrderGoodsRowsImported ||
		!safety.SystemTestsUseDisposableKubernetesPods || !safety.IsolatedInfrastructureStaged ||
		safety.IsolatedAdminRuntimeDeployed || safety.LegacyImportAttempts != 3 ||
		!safety.LegacyImportSucceeded || !safety.PostImportVerifierSucceeded ||
		safety.IsolatedBrowserAcceptanceExecuted {
		t.Fatalf("unsafe legacy verification memory: %#v", status.Spec.Safety)
	}
}

func TestRuntimeCatalogsMatchTheDeclaredOwnership(t *testing.T) {
	content, err := os.ReadFile("../docs/migration/legacy-tables.yaml")
	if err != nil {
		t.Fatalf("read legacy table manifest: %v", err)
	}
	var manifest legacyTableManifest
	if err := yaml.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse legacy table manifest: %v", err)
	}
	targetMall := make(map[string]struct{}, 50)
	targetTenant := make(map[string]struct{}, 1)
	for _, table := range manifest.Spec.Tables {
		if table.Owner == "mall-platform" {
			targetMall[table.Name] = struct{}{}
		}
		if table.Owner == "tenant-platform" && table.TenantScope == "global" {
			targetTenant[table.Name] = struct{}{}
		}
	}
	if len(targetMall) != 50 || len(targetTenant) != 1 {
		t.Fatalf("target compatibility allocation = mall %d tenant %d, want 50/1", len(targetMall), len(targetTenant))
	}
	if _, exists := targetTenant["payments"]; !exists {
		t.Fatalf("tenant target compatibility resource = %#v, want payments", targetTenant)
	}

	mallCatalog, err := os.ReadFile("../apps/mall-platform/web/src/business/legacy/catalog.ts")
	if err != nil {
		t.Fatalf("read mall legacy frontend catalog: %v", err)
	}
	mallDeclaration := regexp.MustCompile(`resource\('[^']+', '([^']+)'\)`)
	matches := mallDeclaration.FindAllSubmatch(mallCatalog, -1)
	currentMall := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := string(match[1])
		if _, duplicate := currentMall[name]; duplicate {
			t.Fatalf("duplicate mall frontend resource %q", name)
		}
		currentMall[name] = struct{}{}
	}
	if len(currentMall) != len(targetMall) {
		t.Fatalf("mall frontend resources = %d, manifest target records %d", len(currentMall), len(targetMall))
	}
	for name := range targetMall {
		if _, exists := currentMall[name]; !exists {
			t.Errorf("mall frontend catalog is missing manifest resource %q", name)
		}
	}
	for name := range currentMall {
		if _, exists := targetMall[name]; !exists {
			t.Errorf("mall frontend catalog contains non-mall resource %q", name)
		}
	}

	tenantCatalog, err := os.ReadFile("../apps/tenant-platform/web/src/business/shared-catalog/catalog.ts")
	if err != nil {
		t.Fatalf("read tenant legacy frontend catalog: %v", err)
	}
	tenantDeclaration := regexp.MustCompile(`resource\(["']([^"']+)["'], (true|false)\)`)
	matches = tenantDeclaration.FindAllSubmatch(tenantCatalog, -1)
	currentTenant := make(map[string]struct{}, len(matches))
	writable := make(map[string]struct{})
	for _, match := range matches {
		name := string(match[1])
		if _, duplicate := currentTenant[name]; duplicate {
			t.Fatalf("duplicate tenant frontend resource %q", name)
		}
		currentTenant[name] = struct{}{}
		if string(match[2]) == "true" {
			writable[name] = struct{}{}
		}
	}
	if len(currentTenant) != len(targetTenant) {
		t.Fatalf("tenant frontend resources = %d, manifest target records %d", len(currentTenant), len(targetTenant))
	}
	if len(writable) != 0 {
		t.Fatalf("tenant payment catalog exposes generic mutation: %#v", writable)
	}

	for name := range targetTenant {
		if _, exists := currentTenant[name]; !exists {
			t.Errorf("tenant frontend catalog is missing manifest resource %q", name)
		}
	}
	for name := range currentTenant {
		if _, exists := targetTenant[name]; !exists {
			t.Errorf("tenant frontend catalog contains non-tenant resource %q", name)
		}
	}
}
