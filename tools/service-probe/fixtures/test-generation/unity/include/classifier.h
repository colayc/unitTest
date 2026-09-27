#pragma once

typedef enum Classification { CLASSIFICATION_NEGATIVE, CLASSIFICATION_ZERO, CLASSIFICATION_POSITIVE } Classification;

Classification classify(int value);
