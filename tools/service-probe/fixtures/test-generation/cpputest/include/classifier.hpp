#pragma once

enum class Classification { negative, zero, positive };

Classification classify(int value) noexcept;
